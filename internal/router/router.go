// Package router picks which model tier serves one agent turn, from the
// shape of the task rather than live provider state.
package router

import "fmt"

// Choice names a provider tier. The three are roles rather than places: a
// machine decides whether each is served on-box or over the network.
type Choice string

const (
	// ChoiceFast serves the turn from the cheapest tier, sized for tool
	// calling and mechanical edits.
	ChoiceFast Choice = "fast"
	// ChoiceBalanced serves the turn from the tier that does most of the
	// work.
	ChoiceBalanced Choice = "balanced"
	// ChoiceDeep serves the turn from the strongest tier, for planning and
	// for what the tier below could not finish.
	ChoiceDeep Choice = "deep"
)

// Default is the tier a turn runs on when nothing pins it. Neither
// neighbor is reached automatically: fast and deep are pinned per thread or
// per run until a task-shape signal exists to choose them (DESIGN.md "Model
// routing").
const Default = ChoiceBalanced

// order is every tier cheapest first, which is also the order a failure
// escalates through.
var order = []Choice{ChoiceFast, ChoiceBalanced, ChoiceDeep}

// Valid reports whether c names a tier Route can serve a turn from. The
// empty Choice is not valid: as an Input.Override it means no override at
// all, which is a caller's decision to make rather than a tier.
func (c Choice) Valid() bool {
	return c == ChoiceFast || c == ChoiceBalanced || c == ChoiceDeep
}

// FastContextBudget is the served window Route assumes for the fast tier
// when an Input names none, matching the llama-server default (DESIGN.md
// "Model routing"). A caller that knows the served window passes it as
// Window.
const FastContextBudget = 8192

// ReplyReserve is the room Route keeps under the served window for the
// model's reply, since a prompt that fills the window leaves the model
// nothing to answer with and llama-server refuses the request outright.
const ReplyReserve = 1024

// Input describes one turn's task shape for routing. It carries no live
// provider state, so Route stays a pure function of it.
type Input struct {
	// Thinking turns a hybrid model's reasoning trace on or off for this
	// turn. Nil leaves the served model's own default, since the runtime
	// flag is meaningful in both states and a request that omits the field
	// must not silently flip it.
	Thinking *bool
	Override Choice
	// Window is the fast tier's served context in tokens, zero for
	// FastContextBudget. The tiers above it are sized in hundreds of
	// thousands of tokens, so only the fast tier's fit is checked.
	Window          int
	EstimatedTokens int
	PriorFailures   int
}

// Decision is a routing outcome: which tier to use and why, so the reason can
// surface directly in the TUI.
type Decision struct {
	Choice Choice
	Reason string
}

// Route picks a Choice for one turn. A turn starts on its Override, or on
// Default when nothing pins it, and a request that would not fit the fast
// tier's served window moves up before anything runs. Each prior failure
// then escalates one tier, so a pin is a floor rather than a cage: a thread
// whose tier keeps failing is worth more than a pin that holds it there.
func Route(in Input) Decision {
	base, reason := Default, "default tier"
	if in.Override != "" {
		base, reason = in.Override, "explicit override"
	}

	if base == ChoiceFast && in.EstimatedTokens > FastBudget(in.Window) {
		base, reason = ChoiceBalanced, "over the fast tier's context budget"
	}

	if in.PriorFailures > 0 {
		if up := escalate(base, in.PriorFailures); up != base {
			return Decision{Choice: up, Reason: fmt.Sprintf("escalated past %s after a failure", base)}
		}

		return Decision{Choice: base, Reason: "no tier above " + string(base)}
	}

	return Decision{Choice: base, Reason: reason}
}

// escalate returns the tier steps above c, stopping at the strongest tier.
// A Choice not in order escalates to nothing, since there is no position to
// count from.
func escalate(c Choice, steps int) Choice {
	for i, t := range order {
		if t == c {
			return order[min(i+steps, len(order)-1)]
		}
	}

	return c
}

// FastBudget is the largest request the fast tier is routed: the served
// window less ReplyReserve, never below zero. Zero window means
// FastContextBudget.
func FastBudget(window int) int {
	if window <= 0 {
		window = FastContextBudget
	}

	return max(window-ReplyReserve, 0)
}

// HostedContextBudget is the served window assumed for the balanced and
// deep tiers. It is the smallest window among the models those tiers are
// pointed at, so a caller sizing compaction against it compacts before the
// smallest of them would refuse the request. It is deliberately not the
// fast tier's window: sizing every tier's compaction from the local one
// compacted a hosted turn at 6k of a window in the hundreds of thousands.
const HostedContextBudget = 128_000

// ContextBudget is the served window of one tier. FastWindow is the fast
// tier's served context, zero for FastContextBudget.
func ContextBudget(c Choice, fastWindow int) int {
	if c == ChoiceFast {
		if fastWindow > 0 {
			return fastWindow
		}

		return FastContextBudget
	}

	return HostedContextBudget
}

// Tiers holds one T per tier, so a caller can wire concrete providers or
// model names to a Decision without this package depending on either type.
type Tiers[T any] struct {
	Fast     T
	Balanced T
	Deep     T
}

// For returns the T belonging to d's tier, falling back to the Default
// tier's value for a Decision naming no tier at all.
//
//nolint:ireturn // T is caller-supplied, not a provider type this package defines or knows about
func (t Tiers[T]) For(d Decision) T {
	switch d.Choice {
	case ChoiceFast:
		return t.Fast
	case ChoiceDeep:
		return t.Deep
	case ChoiceBalanced:
		return t.Balanced
	default:
		return t.Balanced
	}
}
