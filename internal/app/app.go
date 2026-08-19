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
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/openaic"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/mention"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/routine"
	"github.com/kyleking/wavez/internal/runtime"
	"github.com/kyleking/wavez/internal/sched"
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
	ChangeGate      *ChangeGate
	CoverageAdapter *gate.CoverageAdapter
	Routines        *RoutineService
	Store           *codeintel.Store
	Indexer         *codeintel.Indexer
	Leases          *lease.Manager
	Scheduler       *sched.Scheduler
	Mentions        *mention.Expander
	Loop            *agent.Loop
	PlanLoop        *agent.Loop
	GateLog         *gate.Log
	Tools           *tool.Registry
	PlanTools       *tool.Registry
	Scope           *tools.Scope
	Cycles          map[string]cycle.Cycle
	verifier        agent.Verifier
	reviewer        agent.Reviewer
	loopBase        []agent.Option
	sweeper         cycle.Sweeper
	lspPool         *lsp.Pool
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
	Scheduler           *sched.Scheduler
	LocalRuntime        runtime.Config
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

// WithLocalRuntime sets the flags a llama-server wavez starts is served
// with, over the project config's defaults: the per-model settings the
// daemon keeps for the local model. Port and GGUFPath are the supervisor's
// to fill.
func WithLocalRuntime(rc runtime.Config) Option {
	return func(o *Options) { o.LocalRuntime = rc }
}

// WithScheduler shares one memory-aware admission scheduler across every
// App built with it, instead of each App building its own. Memory
// admission answers for the whole laptop, not one project, so a daemon
// serving several projects must pass the same *sched.Scheduler to each
// App it loads or a turn in one project and a gate run in another can
// admit past each other.
func WithScheduler(s *sched.Scheduler) Option {
	return func(o *Options) { o.Scheduler = s }
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

	st, err := openState(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	stateDir, store, gateLog, sandboxDir, prefix := st.dir, st.store, st.gateLog, st.sandboxDir, st.prefix

	// One `go list` per App, shared by test selection and by the blast-radius
	// signal on every permission prompt. A nil graph drops selection to
	// LevelPackage and renders blast as unknown.
	graph, err := gate.BuildImportGraph(ctx, root)
	if err != nil {
		graph = nil
	}

	indexer := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())
	scope := tools.NewScope(options.StrictScope)
	leases := lease.New(root, lease.WithTTL(cfg.LeaseTTL))
	scheduler := options.Scheduler
	if scheduler == nil {
		scheduler = sched.New(sched.WithHeadroom(cfg.AdmissionHeadroom))
	}
	registry := buildRegistry(root, sandboxDir, indexer, store, scope, permGate, options.Asker, leases)

	p := buildProviders(ctx, cfg, options)
	local, hosted, supervisor := p.local, p.hosted, p.supervisor

	lspPool := lsp.NewPool(root)

	bundle, err := buildGates(root, stateDir, store, gateLog, cfg, graph, lspPool, scheduler)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return nil, err
	}

	runner, adapter, verifier := bundle.runner, bundle.adapter, bundle.verifier

	reviewer := NewModelReviewer(root, vcs.NewJj(), local, hosted, cfg.LocalModel, cfg.HostedModel)
	changeGate := NewChangeGate(runner)
	loopBase := loopOptions(root, cfg, options)
	loopOpts := append(append([]agent.Option{}, loopBase...),
		agent.WithVerifier(verifier), agent.WithReviewer(reviewer), agent.WithChangeGate(changeGate))
	loop := agent.New(local, hosted, registry, permGate, loopOpts...)

	sweeper, cycles, err := buildCycles(cfg)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure

		return nil, err
	}
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
	changeGate.Start(bgCtx)
	adapter.Start(bgCtx)

	return &App{
		Root:            root,
		Config:          cfg,
		Store:           store,
		Indexer:         indexer,
		Leases:          leases,
		Scheduler:       scheduler,
		Mentions:        mention.New(root, indexer),
		Scope:           scope,
		lspPool:         lspPool,
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
		ChangeGate:      changeGate,
		CoverageAdapter: adapter,
		Routines:        bundle.routines.service,
		Permission:      permGate,
		Cycles:          cycles,
		verifier:        verifier,
		reviewer:        reviewer,
		loopBase:        loopBase,
		sweeper:         sweeper,
		SandboxDir:      sandboxDir,
		SystemPrefix:    systemPrefix(prefix),
		PlanSystem:      planSystemPrefix(prefix),
		threadLogDir:    filepath.Join(stateDir, threadLogDirName),
	}, nil
}

// projectState is what New opens on disk before it can assemble anything
// else: the state directory, the store, the gate log, the sandbox session
// directory, and the system prefix read from the project's context list.
type projectState struct {
	store      *codeintel.Store
	gateLog    *gate.Log
	dir        string
	sandboxDir string
	prefix     string
}

func openState(ctx context.Context, root string, cfg config.Config) (projectState, error) {
	stateDir := filepath.Join(root, wavezDirName)
	if err := os.MkdirAll(stateDir, dirPerm); err != nil {
		return projectState{}, fmt.Errorf("creating state dir %s: %w", stateDir, err)
	}

	store, err := codeintel.Open(ctx, filepath.Join(stateDir, storeFileName))
	if err != nil {
		return projectState{}, fmt.Errorf("opening code-intelligence store: %w", err)
	}

	gateLog, err := gate.OpenLog(filepath.Join(stateDir, gateLogFileName))
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return projectState{}, fmt.Errorf("opening gate log: %w", err)
	}

	sandboxDir, err := newSessionDir(stateDir)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return projectState{}, err
	}

	prefix, err := BuildPrefix(root, cfg.Context)
	if err != nil {
		_ = store.Close() //nolint:errcheck // best-effort cleanup after a later failure
		return projectState{}, fmt.Errorf("building system prefix: %w", err)
	}

	return projectState{store: store, gateLog: gateLog, dir: stateDir, sandboxDir: sandboxDir, prefix: prefix}, nil
}

// buildCycles resolves the phased ways of working this project can run:
// the built-in fix cycle, plus whatever ".wavez.pkl" defines.
func buildCycles(cfg config.Config) (*cycle.AstGrepSweeper, map[string]cycle.Cycle, error) {
	sweeper := cycle.NewAstGrepSweeper(astgrep.NewRunner())

	cycles, err := cycle.Resolve(cfg.Cycles, cycle.Checks{Prober: cycle.NewGoProber(), Sweeper: sweeper})
	if err != nil {
		return nil, nil, fmt.Errorf("resolving cycles: %w", err)
	}

	return sweeper, cycles, nil
}

// loopOptions maps the Options a caller set to agent.Option values. A zero
// bound means "leave the loop's own default", never "no bound".
func loopOptions(root string, cfg config.Config, options Options) []agent.Option {
	out := []agent.Option{
		agent.WithLocalModel(cfg.LocalModel),
		agent.WithContextWindow(localRuntime(cfg, options).ContextSize),
		agent.WithHostedModel(cfg.HostedModel),
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
		if cfg.LocalBaseURL != "" {
			server = localServer{baseURL: cfg.LocalBaseURL}
		} else if options.ManagedLocalServer {
			server = ensureLocalServer(ctx, cfg, options)
		}

		supervisor = server.supervisor
		local = openaic.New("local", localProviderOptions(ctx, cfg, server.baseURL)...)
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

// localRuntime is what a llama-server wavez starts is served with: the
// per-model settings when the caller has them, else the project config's
// window on the loopback port.
func localRuntime(cfg config.Config, options Options) runtime.Config {
	rc := options.LocalRuntime
	rc.Port = cfg.LocalPort
	if rc.ContextSize <= 0 {
		rc.ContextSize = cfg.ContextWindow
	}

	return rc
}

// localProviderOptions dials baseURL, adding a bearer token only for a remote
// endpoint with a key command, since the loopback server takes none.
func localProviderOptions(ctx context.Context, cfg config.Config, baseURL string) []openaic.Option {
	opts := []openaic.Option{openaic.WithBaseURL(baseURL), openaic.WithModel(cfg.LocalModel)}
	if cfg.LocalBaseURL != "" && cfg.LocalKeyCommand != "" {
		keyFn := func() (string, error) {
			return keyFromCommand(context.WithoutCancel(ctx), "local", cfg.LocalKeyCommand)
		}
		opts = append(opts, openaic.WithAPIKeyFunc(keyFn))
	}

	return opts
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
func ensureLocalServer(ctx context.Context, cfg config.Config, options Options) localServer {
	fallback := runtime.LocalBaseURL(cfg.LocalPort)

	sup := runtime.NewSupervisor(cfg.LocalModel, localRuntime(cfg, options),
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
	permGate permission.Gate, asker tools.Asker, leases tools.Leases,
) *tool.Registry {
	withLeases := tools.WithLeases(leases)

	return tool.NewRegistry(
		tools.NewRead(root, scope),
		tools.NewStrReplace(root, scope, withLeases),
		tools.NewWrite(root, scope, withLeases),
		tools.NewShell(root, sandboxDir, DefaultThreadID, permGate, withLeases),
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
	root, stateDir string, store *codeintel.Store, gateLog *gate.Log, cfg config.Config, graph *gate.ImportGraph,
	lspPool *lsp.Pool, scheduler *sched.Scheduler,
) (gateBundle, error) {
	rules, err := loadConventionRules(root, cfg.AstGrepRules)
	if err != nil {
		return gateBundle{}, err
	}

	// DESIGN.md's gate order: formatter, then convention rules, then the
	// checks that cost real time. Verification adds a whole-module build in
	// front of the test gate, since a compile failure makes every test
	// result noise.
	// One set for the whole project: a change-triggered batch, a
	// verification round, and the background coverage-map build all run
	// `go test` on the same machine.
	resources := gate.NewResourceSet()

	manifestPath := filepath.Join(root, wavezDirName, coverageManifestFileName)
	adapter := gate.NewCoverageAdapter(store, root, manifestPath, goruntime.NumCPU(),
		gate.WithCoverageLog(gateLog), gate.WithCoverageResources(resources))

	convention := gate.NewConventionGate(root, rules, nil)
	// DESIGN.md's gate order puts the type checker last among the checks that
	// fit a per-edit run. Measured on this repo at 1.18 s worst case for a
	// multi-file change, which is why it is here and not in the verification
	// round with the slower checks.
	gates := append(conventionGates(gate.NewFormatGate(root), convention),
		gate.NewLSPGate(root, lspPool), gate.NewGoTestGate(root))

	routines, err := buildRoutines(root, stateDir, cfg, resources,
		append(append([]gate.Gate(nil), gates...), gate.NewBuildGate(root)))
	if err != nil {
		return gateBundle{}, err
	}

	// The adapter, not the store, is what selection reads: only the thing
	// building the map knows whether the map is finished.
	runFunc := gate.BuildRunFunc(gate.RealClock{}, adapter, graph,
		enabledGates(gates, routines.set.DisabledGates()), gateLog, root, resources)
	runner := gate.NewRunner(gate.RealClock{}, cfg.GateDebounce,
		admitted(scheduler, routine.ChangeRunFunc(root, runFunc, routines.runner, routines.compiled)))

	// fail-to-pass runs after go-test because it assumes the suite is green
	// on the tree as written; without that a merely broken test reads as one
	// the revert killed.
	jj := vcs.NewJj()
	verifyGates := append(conventionGates(gate.NewFormatGate(root), convention),
		gate.NewBuildGate(root), gate.NewLSPGate(root, lspPool), gate.NewGoTestGate(root),
		gate.NewFailToPassGate(root, jj, jj))
	verifier := NewGateVerifier(root, adapter, graph, gateLog, gate.RealClock{}, verifyGates, resources)

	return gateBundle{runner: runner, adapter: adapter, verifier: verifier, routines: routines}, nil
}

// enabledGates drops the gates a project turned off by disabling their
// built-in routine, DESIGN.md's "gates are shipped as built-in routines the
// user can override or disable there".
func enabledGates(gates []gate.Gate, disabled []string) []gate.Gate {
	if len(disabled) == 0 {
		return gates
	}

	off := make(map[string]struct{}, len(disabled))
	for _, name := range disabled {
		off[name] = struct{}{}
	}

	out := make([]gate.Gate, 0, len(gates))

	for _, g := range gates {
		if _, skip := off[g.Name()]; !skip {
			out = append(out, g)
		}
	}

	return out
}

// admitted holds a gate run until the scheduler says the machine has room
// for it beside whatever the local model is doing.
func admitted(scheduler *sched.Scheduler, run gate.RunFunc) gate.RunFunc {
	return func(ctx context.Context, changes []tool.Change) gate.RunResult {
		release, err := scheduler.AdmitGate(ctx)
		if err != nil {
			return gate.RunResult{LogError: err, Changes: changes}
		}
		defer release()

		return run(ctx, changes)
	}
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

	if a.lspPool != nil {
		poolCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), serverStopTimeout)
		if err := a.lspPool.Close(poolCtx); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}

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
