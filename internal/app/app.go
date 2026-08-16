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
	"runtime"
	"sync"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/openaic"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
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
)

// App is one project's assembled object graph. Construct it with New and
// release it with Close; do not copy it after construction.
type App struct {
	Local           llm.Provider
	Permission      permission.Gate
	Hosted          llm.Provider
	GateRunner      *gate.Runner
	CoverageAdapter *gate.CoverageAdapter
	Store           *codeintel.Store
	Loop            *agent.Loop
	GateLog         *gate.Log
	Tools           *tool.Registry
	threadLogDir    string
	SandboxDir      string
	SystemPrefix    string
	Root            string
	threads         []*thread.Thread
	Config          config.Config
	mu              sync.Mutex
	closed          bool
}

// Options configures New.
type Options struct {
	Local, Hosted llm.Provider
	Asker         tools.Asker
}

// Option configures an Options.
type Option func(*Options)

// WithProviders overrides the local and hosted llm.Provider App would
// otherwise build against llama-server and OpenRouter. Tests should always
// use this with internal/llm/fake, never a real model.
func WithProviders(local, hosted llm.Provider) Option {
	return func(o *Options) { o.Local, o.Hosted = local, hosted }
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

	registry := buildRegistry(root, sandboxDir, store, permGate, options.Asker)

	local, hosted := options.Local, options.Hosted
	if local == nil {
		local = openaic.New("local", openaic.WithBaseURL(DefaultLocalBaseURL), openaic.WithModel(cfg.LocalModel))
	}

	if hosted == nil {
		hosted = openaic.New("hosted",
			openaic.WithBaseURL(DefaultHostedBaseURL),
			openaic.WithModel(cfg.HostedModel),
			openaic.WithAPIKey(os.Getenv(HostedAPIKeyEnv)))
	}

	runner, adapter, verifier := buildGateRunner(ctx, root, store, gateLog, cfg)

	loop := agent.New(local, hosted, registry, permGate,
		agent.WithLocalModel(cfg.LocalModel), agent.WithHostedModel(cfg.HostedModel), agent.WithVerifier(verifier))

	return &App{
		Root:            root,
		Config:          cfg,
		Store:           store,
		Tools:           registry,
		Local:           local,
		Hosted:          hosted,
		Loop:            loop,
		GateLog:         gateLog,
		GateRunner:      runner,
		CoverageAdapter: adapter,
		Permission:      permGate,
		SandboxDir:      sandboxDir,
		SystemPrefix:    systemPrefix(prefix),
		threadLogDir:    filepath.Join(stateDir, threadLogDirName),
	}, nil
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
	root, sandboxDir string, store *codeintel.Store, permGate permission.Gate, asker tools.Asker,
) *tool.Registry {
	return tool.NewRegistry(
		tools.NewRead(root),
		tools.NewStrReplace(root),
		tools.NewWrite(root),
		tools.NewShell(root, sandboxDir, DefaultThreadID, permGate),
		tools.NewSearch(store),
		tools.NewQuestion(asker),
	)
}

func buildGateRunner(
	ctx context.Context, root string, store *codeintel.Store, gateLog *gate.Log, cfg config.Config,
) (*gate.Runner, *gate.CoverageAdapter, *GateVerifier) {
	graph, err := gate.BuildImportGraph(ctx, root)
	if err != nil {
		graph = nil
	}

	gates := []gate.Gate{gate.NewFormatGate(root), gate.NewGoTestGate(root)}
	runFunc := gate.BuildRunFunc(gate.RealClock{}, store, graph, gates, gateLog, root)
	runner := gate.NewRunner(gate.RealClock{}, cfg.GateDebounce, runFunc)

	manifestPath := filepath.Join(root, wavezDirName, coverageManifestFileName)
	adapter := gate.NewCoverageAdapter(store, manifestPath, runtime.NumCPU())

	verifyGates := []gate.Gate{gate.NewFormatGate(root), gate.NewBuildGate(root), gate.NewGoTestGate(root)}
	verifier := NewGateVerifier(root, store, graph, gateLog, gate.RealClock{}, verifyGates)

	return runner, adapter, verifier
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

	var errs []error

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
