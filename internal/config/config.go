// Package config loads a project's ".wavez.pkl" through a single long-lived
// pkl-go evaluator (~130 µs warm per DESIGN.md's pkl decision) and falls
// back to fixed defaults when no config file exists, so a fresh repo works
// with zero setup. Wavez never auto-loads AGENTS.md, CLAUDE.md, .agents/, or
// .claude/: the only ways a file enters the agent's context are the
// project's explicit Context list and a caller-supplied --with override.
package config

import (
	"time"

	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/routine"
)

// DefaultFastModel is the fast tier's model when no config overrides it,
// served on-box. Reliable tool calling is the binding constraint on this
// choice, not size: a model that renders calls as prose is worth nothing to
// the tier that exists to make them.
const DefaultFastModel = "qwen3:8b"

// DefaultBalancedModel and DefaultDeepModel are the two network tiers'
// models when no config overrides them.
const (
	DefaultBalancedModel = "stealth/ox-alpha"
	DefaultDeepModel     = "stealth/ox-alpha"
)

// DefaultContextWindow is the served context window, in tokens, of a
// llama-server wavez starts for the fast tier, matching
// internal/router.FastContextBudget and llama-server's own default.
const DefaultContextWindow = 8192

// DefaultLocalPort is the loopback port llama-server serves the fast tier's
// model on, matching internal/runtime.DefaultPort.
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
	// Routines are the project's routine definitions, keyed by name, before
	// they are compiled against an action registry. A routine named here
	// replaces the built-in of the same name outright.
	Routines map[string]routine.Definition
	Root     string
	// Tiers is which model answers each routing tier and where it is served.
	Tiers router.Tiers[Tier]
	// HostedKeyCommand's stdout is the API key for any tier dialing a
	// network endpoint with no key command of its own. Empty means fall back
	// to the OPENROUTER_API_KEY environment variable.
	HostedKeyCommand string
	// WebSearchURL names a SearxNG instance for the web search tool, empty
	// to search through DuckDuckGo's HTML endpoint.
	WebSearchURL string
	Context      []string
	ExtraDirs    []string
	// Cycles are the phased ways of working this project defines, beside the
	// ones wavez ships. A definition here replaces a built-in of the same
	// name outright.
	Cycles        []cycle.Spec
	AstGrepRules  []string
	DeadcodeAllow []string
	// Links are this project's identifier-to-URL patterns (PR numbers, issue
	// keys, ticket ids), matched against transcript and -p output text and
	// rendered as hyperlinks. Entries here take precedence over the per-laptop
	// user config on a clash.
	Links []LinkPattern
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
	// LocalPort is the loopback port llama-server serves the fast tier's
	// model on. Wavez reuses a server already answering there rather than
	// starting a second one.
	LocalPort int
	// LocalStartTimeout bounds one llama-server start attempt.
	LocalStartTimeout time.Duration
	// Web offers the search and fetch tools at all. It is off by default
	// for two reasons: the pair costs 217 preamble tokens on every turn of
	// every thread, and a coding agent that can reach the network without
	// being asked to is a wider exposure than one that cannot.
	Web bool
}

// Tier is one routing tier's model and the OpenAI-compatible endpoint it is
// served from. An empty BaseURL means the tier's default endpoint: the
// llama-server on LocalPort for the fast tier, OpenRouter for the others.
type Tier struct {
	Model string
	// BaseURL points this tier at a server elsewhere. When the fast tier
	// sets it, Wavez neither starts nor stops a llama-server and LocalPort
	// is unused.
	BaseURL string
	// KeyCommand's stdout is this tier's bearer token, overriding
	// HostedKeyCommand. Empty means none for the loopback server, and
	// HostedKeyCommand for any other endpoint.
	KeyCommand string
}

// LinkPattern is one identifier-linking rule: text matching Pattern renders
// as a link to URL, which may reference the pattern's capture groups with
// Go regexp.Expand syntax ("$1").
type LinkPattern struct {
	Pattern string
	URL     string
}

// Defaults returns the Config a project with no ".wavez.pkl" gets: every
// field is readable without a config file at all.
func Defaults(root string) Config {
	return Config{
		Root: root,
		Tiers: router.Tiers[Tier]{
			Fast:     Tier{Model: DefaultFastModel},
			Balanced: Tier{Model: DefaultBalancedModel},
			Deep:     Tier{Model: DefaultDeepModel},
		},
		ContextWindow:     DefaultContextWindow,
		GateDebounce:      DefaultGateDebounce,
		FullRunCadence:    DefaultFullRunCadence,
		HookTimeout:       DefaultHookTimeout,
		AdmissionHeadroom: DefaultAdmissionHeadroom,
		LeaseTTL:          DefaultLeaseTTL,
		LocalPort:         DefaultLocalPort,
		LocalStartTimeout: DefaultLocalStartTimeout,
		Web:               false,
	}
}
