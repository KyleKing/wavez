// Package router picks which model tier serves one agent turn, from the
// shape of the task rather than live provider state.
package router

// Choice names a provider tier.
type Choice string

const (
	// ChoiceLocal serves the turn from the local model.
	ChoiceLocal Choice = "local"
	// ChoiceHosted escalates the turn to a hosted model.
	ChoiceHosted Choice = "hosted"
)

// Valid reports whether c names a tier Route can serve a turn from. The
// empty Choice is not valid: as an Input.Override it means no override at
// all, which is a caller's decision to make rather than a tier.
func (c Choice) Valid() bool {
	return c == ChoiceLocal || c == ChoiceHosted
}

// LocalContextBudget is the token budget above which a turn escalates to
// hosted, matching the served context of the v0.1 llama-server runtime
// (DESIGN.md "Model routing").
const LocalContextBudget = 8000

// Input describes one turn's task shape for routing. It carries no live
// provider state, so Route stays a pure function of it.
type Input struct {
	// Thinking turns a hybrid model's reasoning trace on or off for this
	// turn. Nil leaves the served model's own default, since the runtime
	// flag is meaningful in both states and a request that omits the field
	// must not silently flip it.
	Thinking        *bool
	Override        Choice
	FileCount       int
	EstimatedTokens int
	PriorFailures   int
}

// Decision is a routing outcome: which tier to use and why, so the reason can
// surface directly in the TUI.
type Decision struct {
	Choice Choice
	Reason string
}

// Route picks a Choice for one turn. An explicit Override always wins;
// otherwise any prior local failure escalates immediately (local is never
// retried past one failure), then a multi-file task or one over
// LocalContextBudget escalates, and everything else runs local.
func Route(in Input) Decision {
	if in.Override != "" {
		return Decision{Choice: in.Override, Reason: "explicit override"}
	}
	if in.PriorFailures > 0 {
		return Decision{Choice: ChoiceHosted, Reason: "prior local failure"}
	}
	if in.FileCount > 1 {
		return Decision{Choice: ChoiceHosted, Reason: "multi-file task"}
	}
	if in.EstimatedTokens > LocalContextBudget {
		return Decision{Choice: ChoiceHosted, Reason: "over local context budget"}
	}

	return Decision{Choice: ChoiceLocal, Reason: "single-file task within local context budget"}
}

// Select returns hosted when d.Choice is ChoiceHosted and local otherwise, so
// callers can wire concrete providers to a Decision without this package
// depending on any provider type.
//
//nolint:ireturn // T is caller-supplied, not a provider type this package defines or knows about
func Select[T any](d Decision, local, hosted T) T {
	if d.Choice == ChoiceHosted {
		return hosted
	}

	return local
}
