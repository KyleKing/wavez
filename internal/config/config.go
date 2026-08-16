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

// DefaultHostedModel is the hosted model name used when no config overrides it.
const DefaultHostedModel = "anthropic/claude-sonnet-4"

// DefaultContextWindow is the local model's served context window, in
// tokens, matching internal/router.LocalContextBudget.
const DefaultContextWindow = 8000

// DefaultGateDebounce is how long a Runner waits after the last change in a
// burst before invoking gates.
const DefaultGateDebounce = 500 * time.Millisecond

// DefaultFullRunCadence is how many selective gate passes are allowed
// before a full run is forced.
const DefaultFullRunCadence = 20

// Config is one project's Wavez configuration: what DESIGN.md's "Project
// instructions" and "Gates (v0.1)" sections say v0.1 reads, and no more.
type Config struct {
	Root           string
	LocalModel     string
	HostedModel    string
	Context        []string
	ExtraDirs      []string
	AstGrepRules   []string
	ContextWindow  int
	GateDebounce   time.Duration
	FullRunCadence int
}

// Defaults returns the Config a project with no ".wavez.pkl" gets: every
// field is readable without a config file at all.
func Defaults(root string) Config {
	return Config{
		Root:           root,
		LocalModel:     DefaultLocalModel,
		HostedModel:    DefaultHostedModel,
		ContextWindow:  DefaultContextWindow,
		GateDebounce:   DefaultGateDebounce,
		FullRunCadence: DefaultFullRunCadence,
	}
}
