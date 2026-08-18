// Package config loads a project's ".wavez.pkl" through a single long-lived
// pkl-go evaluator (~130 µs warm per DESIGN.md's pkl decision) and falls
// back to fixed defaults when no config file exists, so a fresh repo works
// with zero setup. Wavez never auto-loads AGENTS.md, CLAUDE.md, .agents/, or
// .claude/: the only ways a file enters the agent's context are the
// project's explicit Context list and a caller-supplied --with override.
package config

import "time"

// DefaultLocalModel is the local model name used when no config overrides it.
const DefaultLocalModel = "qwen3:8b"

// DefaultHostedModel is the hosted model name used when no config overrides
// it. Reliable tool calling is the binding constraint on this choice, not
// price: a model that renders calls as prose escalates to nothing.
const DefaultHostedModel = "openai/gpt-5-mini"

// DefaultContextWindow is the local model's served context window, in
// tokens, matching internal/router.LocalContextBudget.
const DefaultContextWindow = 8000

// DefaultLocalPort is the loopback port llama-server serves the local model
// on, matching internal/runtime.DefaultPort.
const DefaultLocalPort = 8080

// DefaultLocalStartTimeout bounds one llama-server start. Cold start
// measured 1.6 s to first token on this laptop; the rest of the budget
// covers reading a 5 GB blob past a cold page cache.
const DefaultLocalStartTimeout = 60 * time.Second

// DefaultGateDebounce is how long a Runner waits after the last change in a
// burst before invoking gates.
const DefaultGateDebounce = 500 * time.Millisecond

// DefaultFullRunCadence is how many selective gate passes are allowed
// before a full run is forced.
const DefaultFullRunCadence = 20

// DefaultAdmissionHeadroom is the free-memory fraction at or above which a
// local turn and a gate run may overlap, matching internal/sched.
const DefaultAdmissionHeadroom = 0.25

// DefaultLeaseTTL bounds how long a write lease survives unrenewed, matching
// internal/lease.
const DefaultLeaseTTL = 30 * time.Minute

// DefaultHookTimeout bounds one pre- or post-tool-use hook process, matching
// internal/hook.DefaultTimeout.
const DefaultHookTimeout = 5 * time.Second

// Config is one project's Wavez configuration: what DESIGN.md's "Project
// instructions" and "Gates (v0.1)" sections say v0.1 reads, and no more.
type Config struct {
	Root        string
	LocalModel  string
	HostedModel string
	// HostedKeyCommand's stdout is the hosted API key. Empty means fall back
	// to the OPENROUTER_API_KEY environment variable.
	HostedKeyCommand string
	// LocalBaseURL points the local tier at a llama-server elsewhere. When
	// set, Wavez neither starts nor stops a server and LocalPort is unused.
	LocalBaseURL string
	// LocalKeyCommand's stdout is the bearer token for LocalBaseURL. Empty
	// means none.
	LocalKeyCommand string
	Context         []string
	ExtraDirs       []string
	AstGrepRules    []string
	DeadcodeAllow   []string
	// PreToolUseHook and PostToolUseHook are argv slices, program first,
	// executed directly rather than through a shell. Empty means no hook, and
	// no hook process.
	PreToolUseHook  []string
	PostToolUseHook []string
	ContextWindow   int
	GateDebounce    time.Duration
	// AdmissionHeadroom is the free-memory fraction at or above which a turn
	// on the local model and a gate run may overlap.
	AdmissionHeadroom float64
	// LeaseTTL bounds how long a write lease survives unrenewed.
	LeaseTTL       time.Duration
	FullRunCadence int
	// HookTimeout bounds one hook process. A pre-tool-use hook that exceeds
	// it refuses the call.
	HookTimeout time.Duration
	// LocalPort is the loopback port llama-server serves LocalModel on.
	// Wavez reuses a server already answering there rather than starting a
	// second one.
	LocalPort int
	// LocalStartTimeout bounds one llama-server start attempt.
	LocalStartTimeout time.Duration
}

// Defaults returns the Config a project with no ".wavez.pkl" gets: every
// field is readable without a config file at all.
func Defaults(root string) Config {
	return Config{
		Root:              root,
		LocalModel:        DefaultLocalModel,
		HostedModel:       DefaultHostedModel,
		ContextWindow:     DefaultContextWindow,
		GateDebounce:      DefaultGateDebounce,
		FullRunCadence:    DefaultFullRunCadence,
		HookTimeout:       DefaultHookTimeout,
		AdmissionHeadroom: DefaultAdmissionHeadroom,
		LeaseTTL:          DefaultLeaseTTL,
		LocalPort:         DefaultLocalPort,
		LocalStartTimeout: DefaultLocalStartTimeout,
	}
}
