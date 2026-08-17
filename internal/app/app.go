// Package app is the composition root: it builds one project's full object
// graph so cmd/wavez and cmd/wavezd assemble it identically and neither
// owns wiring. Everything it constructs is returned on App; nothing here is
// package-level state.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/astgrep"
	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/openaic"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/runtime"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
	"github.com/kyleking/wavez/internal/vcs"
)

const (
	// DefaultThreadID identifies the one thread v0.1's Shell tool attaches
	// to permission requests. Per-thread tool registries are v0.2 work
	// (DESIGN.md's v0.1 phase is single-thread); until then every approval
	// prompt from any thread in this App reports the same thread ID.
	DefaultThreadID = "wavez"

	// DefaultLocalBaseURL is llama-server's default OpenAI-compatible
	// endpoint (DESIGN.md's local runtime decision).
	DefaultLocalBaseURL = "http://127.0.0.1:8080/v1"
	// DefaultHostedBaseURL is OpenRouter's OpenAI-compatible endpoint
	// (DESIGN.md's router decision).
	DefaultHostedBaseURL = "https://openrouter.ai/api/v1"
	// HostedAPIKeyEnv is the environment variable holding the OpenRouter
	// API key for the default hosted provider.
	//nolint:gosec // this names an env var, it does not hold a credential
	HostedAPIKeyEnv = "OPENROUTER_API_KEY"

	wavezDirName             = ".wavez"
	storeFileName            = "index.db"
	gateLogFileName          = "gate.log"
	coverageManifestFileName = "coverage-manifest.json"
	threadLogDirName         = "threads"
	sessionsDirName          = "sessions"

	dirPerm = 0o755

	// Close waits this long for llama-server to exit on SIGTERM before
	// killing it, because a leaked server holds the model's memory.
	serverStopTimeout = 10 * time.Second

	// Deterministic compaction is tuned for an 8k served window: keep enough
	// of a tool result to read a stack frame or a test name, and hold a
	// result in full only while the turn that asked for it is still recent.
	compactKeepLines  = 20
	compactMaxToolAge = 4
)

// ReadOnlyTools names the tools a plan thread may call: the ones that
// answer questions about the tree without changing it. Shell is absent
// because no deterministic check decides whether a command a model wrote
// only reads, which is the same reason DESIGN.md puts shell behind the
// permission gate.
var ReadOnlyTools = []string{"read", "search", "context", "question"}

// App is one project's assembled object graph. Construct it with New and
// release it with Close; do not copy it after construction.
type App struct {
	Local           llm.Provider
	Permission      permission.Gate
	Hosted          llm.Provider
	GateRunner      *gate.Runner
	CoverageAdapter *gate.CoverageAdapter
	Store           *codeintel.Store
	Indexer         *codeintel.Indexer
	Loop            *agent.Loop
	PlanLoop        *agent.Loop
	GateLog         *gate.Log
	Tools           *tool.Registry
	PlanTools       *tool.Registry
	Scope           *tools.Scope
	supervisor      *runtime.Supervisor
	bgCancel        context.CancelFunc
	threadLogDir    string
	SandboxDir      string
	SystemPrefix    string
	PlanSystem      string
	Root            string
	threads         []*thread.Thread
	Config          config.Config
	mu              sync.Mutex
	closed          bool
}

// Options configures New.
type Options struct {
	Local, Hosted       llm.Provider
	Asker               tools.Asker
	MaxTurns            int
	MaxToolCallsPerTurn int
	MaxStagnantErrors   int
	MaxWallClock        time.Duration
	MaxHostedSpendUSD   float64
	StrictScope         bool
	ManagedLocalServer  bool
}

// Option configures an Options.
type Option func(*Options)

// WithProviders overrides the local and hosted llm.Provider App would
// otherwise build against llama-server and OpenRouter. Tests should always
// use this with internal/llm/fake, never a real model.
func WithProviders(local, hosted llm.Provider) Option {
	return func(o *Options) { o.Local, o.Hosted = local, hosted }
}

// WithMaxTurns bounds model turns for every thread this App builds.
func WithMaxTurns(n int) Option {
	return func(o *Options) { o.MaxTurns = n }
}

// WithMaxToolCallsPerTurn bounds tool calls within a single model turn for
// every thread this App builds.
func WithMaxToolCallsPerTurn(n int) Option {
	return func(o *Options) { o.MaxToolCallsPerTurn = n }
}

// WithMaxWallClock bounds one run's total wall-clock time for every thread
// this App builds.
func WithMaxWallClock(d time.Duration) Option {
	return func(o *Options) { o.MaxWallClock = d }
}

// WithMaxHostedSpendUSD bounds one run's accumulated hosted-tier spend for
// every thread this App builds.
func WithMaxHostedSpendUSD(v float64) Option {
	return func(o *Options) { o.MaxHostedSpendUSD = v }
}

// WithMaxStagnantErrors bounds consecutive erroring tool-call results for
// every thread this App builds.
func WithMaxStagnantErrors(n int) Option {
	return func(o *Options) { o.MaxStagnantErrors = n }
}

// WithManagedLocalServer lets App start llama-server for the configured
// local model when nothing is already serving it, and stop it on Close.
// Off by default because loading a model costs seconds and gigabytes, and
// constructing an App is not on its own a reason to pay that.
func WithManagedLocalServer() Option {
	return func(o *Options) { o.ManagedLocalServer = true }
}

// WithStrictScope refuses an edit to a file the run has neither read nor
// created, instead of recording it and allowing it.
func WithStrictScope() Option {
	return func(o *Options) { o.StrictScope = true }
}

// WithAsker sets the Asker backing the question tool. A headless run and
// the TUI each supply a different one; the default refuses every question.
func WithAsker(asker tools.Asker) Option {
	return func(o *Options) { o.Asker = asker }
}

// New builds the full object graph for the project at root, configured by
// cfg. PermGate is consulted for any tool call that needs approval; a
// headless run and the TUI each supply a different one. The returned App
// owns a codeintel store, a gate log, and a sandbox session dir, all
// released together by Close.
func New(ctx context.Context, root string, cfg config.Config, permGate permission.Gate, opts ...Option) (*App, error) {
	options := Options{Asker: refuseAsker{}}
	for _, opt := range opts {
		opt(&options)
	}

	stateDir := filepath.Join(root, wavezDirName)
	if err := os.MkdirAll(stateDir, dirPerm); err != nil {
		return nil, fmt.Errorf("creating state dir %s: %w", stateDir, err)
	}

	store, err := codeintel.Open(ctx, filepath.Join(stateDir, storeFileName))
	if err != nil {
		return nil, fmt.Errorf("opening code-intelligence store: %w", err)
	}

	gateLog, err := gate.OpenLog(filepath.Join(stateDir, gateLogFileName))
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return nil, fmt.Errorf("opening gate log: %w", err)
	}

	sandboxDir, err := newSessionDir(stateDir)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return nil, err
	}

	prefix, err := BuildPrefix(root, cfg.Context)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return nil, fmt.Errorf("building system prefix: %w", err)
	}

	// One `go list` per App, shared by test selection and by the blast-radius
	// signal on every permission prompt. A nil graph drops selection to
	// LevelPackage and renders blast as unknown.
	graph, err := gate.BuildImportGraph(ctx, root)
	if err != nil {
		graph = nil
	}

	indexer := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())
	scope := tools.NewScope(options.StrictScope)
	registry := buildRegistry(root, sandboxDir, indexer, store, scope, permGate, options.Asker)

	p := buildProviders(ctx, cfg, options)
	local, hosted, supervisor := p.local, p.hosted, p.supervisor

	runner, adapter, verifier, err := buildGates(root, store, gateLog, cfg, graph)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return nil, err
	}

	reviewer := NewModelReviewer(root, vcs.NewJj(), local, hosted, cfg.LocalModel, cfg.HostedModel)
	loopOpts := loopOptions(root, cfg, options, verifier, reviewer)
	loop := agent.New(local, hosted, registry, permGate, loopOpts...)
	// Plan mode is a thread whose tools are read-only rather than a mode the
	// loop knows about, so it is the same loop over a narrower registry.
	// Narrowing the registry and not just the advertised specs matters: a
	// model that names an unadvertised tool would otherwise still reach it.
	planRegistry := registry.Only(ReadOnlyTools...)
	planLoop := agent.New(local, hosted, planRegistry, permGate, loopOpts...)

	// Detached from ctx on purpose: the first index outlives whatever
	// request built the App, and Close is what ends it.
	bgCtx, bgCancel := context.WithCancel(context.WithoutCancel(ctx))
	indexer.Start(bgCtx)

	return &App{
		Root:            root,
		Config:          cfg,
		Store:           store,
		Indexer:         indexer,
		Scope:           scope,
		supervisor:      supervisor,
		bgCancel:        bgCancel,
		Tools:           registry,
		Local:           local,
		Hosted:          hosted,
		Loop:            loop,
		PlanLoop:        planLoop,
		PlanTools:       planRegistry,
		GateLog:         gateLog,
		GateRunner:      runner,
		CoverageAdapter: adapter,
		Permission:      permGate,
		SandboxDir:      sandboxDir,
		SystemPrefix:    systemPrefix(prefix),
		PlanSystem:      planSystemPrefix(prefix),
		threadLogDir:    filepath.Join(stateDir, threadLogDirName),
	}, nil
}

// loopOptions maps the Options a caller set to agent.Option values. A zero
// bound means "leave the loop's own default", never "no bound".
func loopOptions(
	root string, cfg config.Config, options Options, verifier agent.Verifier, reviewer agent.Reviewer,
) []agent.Option {
	out := []agent.Option{
		agent.WithLocalModel(cfg.LocalModel),
		agent.WithHostedModel(cfg.HostedModel),
		agent.WithVerifier(verifier),
		agent.WithReviewer(reviewer),
		agent.WithCheckpointer(vcs.NewJj(), root),
		agent.WithHooks(hook.New(root,
			hook.WithPreToolUse(cfg.PreToolUseHook...),
			hook.WithPostToolUse(cfg.PostToolUseHook...),
			hook.WithTimeout(cfg.HookTimeout))),
		agent.WithCompaction(thread.CompactOptions{
			KeepLines:   compactKeepLines,
			MaxToolAge:  compactMaxToolAge,
			DedupeReads: true,
		}, agent.DefaultCompactTrigger),
	}

	if options.MaxTurns > 0 {
		out = append(out, agent.WithMaxTurns(options.MaxTurns))
	}

	if options.MaxToolCallsPerTurn > 0 {
		out = append(out, agent.WithMaxToolCallsPerTurn(options.MaxToolCallsPerTurn))
	}

	if options.MaxWallClock > 0 {
		out = append(out, agent.WithMaxWallClock(options.MaxWallClock))
	}

	if options.MaxHostedSpendUSD > 0 {
		out = append(out, agent.WithMaxHostedSpendUSD(options.MaxHostedSpendUSD))
	}

	if options.MaxStagnantErrors > 0 {
		out = append(out, agent.WithMaxStagnantErrors(options.MaxStagnantErrors))
	}

	return out
}

// buildProviders resolves the two model tiers, starting a local server when
// the caller asked App to manage one. It returns the supervisor only when
// wavez started the server, since one it merely found belongs to someone
// else.
func buildProviders(ctx context.Context, cfg config.Config, options Options) providers {
	local, hosted := options.Local, options.Hosted

	var supervisor *runtime.Supervisor

	if local == nil {
		server := localServer{baseURL: runtime.LocalBaseURL(cfg.LocalPort)}
		if options.ManagedLocalServer {
			server = ensureLocalServer(ctx, cfg)
		}

		supervisor = server.supervisor
		local = openaic.New("local", openaic.WithBaseURL(server.baseURL), openaic.WithModel(cfg.LocalModel))
	}

	if hosted == nil {
		// Resolved on first hosted request, not here: a local-only run must not
		// require a credential it never uses.
		keyFn := func() (string, error) { return hostedKey(context.WithoutCancel(ctx), cfg.HostedKeyCommand) }
		hosted = openaic.New("hosted",
			openaic.WithBaseURL(DefaultHostedBaseURL),
			openaic.WithModel(cfg.HostedModel),
			openaic.WithAPIKeyFunc(keyFn))
	}

	return providers{local: local, hosted: hosted, supervisor: supervisor}
}

// providers is the two model tiers plus the supervisor to stop, non-nil
// only when wavez started the local server itself.
type providers struct {
	local      llm.Provider
	hosted     llm.Provider
	supervisor *runtime.Supervisor
}

// ensureLocalServer starts llama-server for the configured local model, or
// reuses one already answering on its port. A start failure is not fatal:
// the caller may still have a server wavez did not start, so the reason is
// reported and the default endpoint is returned to try anyway.
func ensureLocalServer(ctx context.Context, cfg config.Config) localServer {
	fallback := runtime.LocalBaseURL(cfg.LocalPort)

	sup := runtime.NewSupervisor(cfg.LocalModel, runtime.Config{Port: cfg.LocalPort},
		runtime.WithStartTimeout(cfg.LocalStartTimeout))

	endpoint, err := sup.Ensure(context.WithoutCancel(ctx))
	if err != nil {
		fmt.Fprintf(os.Stderr, "wavez: local model unavailable, trying %s anyway: %v\n", fallback, err)

		return localServer{baseURL: fallback}
	}

	if !endpoint.Managed {
		return localServer{baseURL: endpoint.BaseURL}
	}

	return localServer{supervisor: sup, baseURL: endpoint.BaseURL}
}

// localServer is the endpoint the local provider dials, plus the supervisor
// to stop if wavez is the one that started it. A nil supervisor means the
// server belongs to someone else and must be left running.
type localServer struct {
	supervisor *runtime.Supervisor
	baseURL    string
}

func newSessionDir(stateDir string) (string, error) {
	sessionsRoot := filepath.Join(stateDir, sessionsDirName)
	if err := os.MkdirAll(sessionsRoot, dirPerm); err != nil {
		return "", fmt.Errorf("creating sessions dir %s: %w", sessionsRoot, err)
	}

	dir, err := os.MkdirTemp(sessionsRoot, "session-")
	if err != nil {
		return "", fmt.Errorf("creating session dir: %w", err)
	}

	return dir, nil
}

func buildRegistry(
	root, sandboxDir string, indexer *codeintel.Indexer, store *codeintel.Store, scope *tools.Scope,
	permGate permission.Gate, asker tools.Asker,
) *tool.Registry {
	return tool.NewRegistry(
		tools.NewRead(root, scope),
		tools.NewStrReplace(root, scope),
		tools.NewWrite(root, scope),
		tools.NewShell(root, sandboxDir, DefaultThreadID, permGate),
		tools.NewSearch(indexer),
		tools.NewContext(tools.StoreIndex{Indexer: indexer, Store: store}),
		tools.NewQuestion(asker),
	)
}

// conventionGates returns format plus convention when the project
// configured rules, and format alone when it did not.
func conventionGates(format *gate.FormatGate, convention *gate.ConventionGate) []gate.Gate {
	if convention == nil {
		return []gate.Gate{format}
	}

	return []gate.Gate{format, convention}
}

// loadConventionRules expands globs against the project root rather than
// the daemon's working directory. A rule file that will not parse is a
// configuration error, not a reason to run with convention checks silently
// off.
func loadConventionRules(root string, patterns []string) ([]astgrep.RuleFile, error) {
	rules, err := astgrep.LoadRuleFiles(rootedGlobs(root, patterns))
	if err != nil {
		return nil, fmt.Errorf("loading ast-grep rules: %w", err)
	}

	return rules, nil
}

func rootedGlobs(root string, patterns []string) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		if filepath.IsAbs(p) {
			out[i] = p

			continue
		}

		out[i] = filepath.Join(root, p)
	}

	return out
}

// buildGates loads the project's convention rules and assembles both gate
// pipelines against them.
func buildGates(
	root string, store *codeintel.Store, gateLog *gate.Log, cfg config.Config, graph *gate.ImportGraph,
) (*gate.Runner, *gate.CoverageAdapter, *GateVerifier, error) {
	rules, err := loadConventionRules(root, cfg.AstGrepRules)
	if err != nil {
		return nil, nil, nil, err
	}

	// DESIGN.md's gate order: formatter, then convention rules, then the
	// checks that cost real time. Verification adds a whole-module build in
	// front of the test gate, since a compile failure makes every test
	// result noise.
	convention := gate.NewConventionGate(root, rules, nil)
	gates := append(conventionGates(gate.NewFormatGate(root), convention), gate.NewGoTestGate(root))
	runFunc := gate.BuildRunFunc(gate.RealClock{}, store, graph, gates, gateLog, root)
	runner := gate.NewRunner(gate.RealClock{}, cfg.GateDebounce, runFunc)

	manifestPath := filepath.Join(root, wavezDirName, coverageManifestFileName)
	adapter := gate.NewCoverageAdapter(store, manifestPath, goruntime.NumCPU())

	// fail-to-pass runs after go-test because it assumes the suite is green
	// on the tree as written; without that a merely broken test reads as one
	// the revert killed.
	jj := vcs.NewJj()
	verifyGates := append(conventionGates(gate.NewFormatGate(root), convention),
		gate.NewBuildGate(root), gate.NewGoTestGate(root), gate.NewFailToPassGate(root, jj, jj))
	verifier := NewGateVerifier(root, store, graph, gateLog, gate.RealClock{}, verifyGates)

	return runner, adapter, verifier, nil
}

// OpenThread opens or resumes a thread under this App's thread log
// directory and tracks it so Close releases its event log too.
func (a *App) OpenThread(id thread.ID, dirs []string, opts ...thread.Option) (*thread.Thread, error) {
	t, err := thread.Open(a.threadLogDir, id, dirs, opts...)
	if err != nil {
		return nil, fmt.Errorf("opening thread %s: %w", id, err)
	}

	a.mu.Lock()
	a.threads = append(a.threads, t)
	a.mu.Unlock()

	return t, nil
}

// Close releases the store, every thread's event log, and the sandbox
// session dir, in that order. Safe to call more than once; only the first
// call does anything.
func (a *App) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return nil
	}

	a.closed = true

	a.bgCancel()

	var errs []error

	if a.supervisor != nil {
		// A leaked llama-server holds the model's 6 GB, so the stop always
		// carries a deadline and kills past it.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), serverStopTimeout)
		if err := a.supervisor.Stop(stopCtx); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}

	if err := a.Store.Close(); err != nil {
		errs = append(errs, err)
	}

	for _, t := range a.threads {
		if err := t.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if err := os.RemoveAll(a.SandboxDir); err != nil {
		errs = append(errs, fmt.Errorf("removing sandbox dir %s: %w", a.SandboxDir, err))
	}

	return errors.Join(errs...)
}

// refuseAsker is the Asker a caller gets when it supplies none: a headless
// run with no explicit answer policy should fail closed rather than block
// forever on stdin.
type refuseAsker struct{}

var errNoAsker = errors.New("app: no Asker configured for the question tool")

func (refuseAsker) Ask(context.Context, string) (string, error) {
	return "", errNoAsker
}
