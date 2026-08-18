// Package cycle runs a named, phased way of working on a class of problem.
// Each phase gives the model a standing goal and a narrowed tool set, and
// the harness decides whether the phase may advance by evaluating a
// Condition against the tree and its own records. A phase never advances on
// the model reporting it is done, which is the whole difference between a
// Cycle and a prompt that describes the same steps.
package cycle

import (
	"errors"
	"fmt"

	"github.com/kyleking/wavez/internal/condition"
	"github.com/kyleking/wavez/internal/tool"
)

// DefaultMaxAttempts bounds how many times one phase's Loop may run before
// the Cycle ends with that phase's Condition unmet.
const DefaultMaxAttempts = 2

// Sentinel errors a malformed Cycle definition produces.
var (
	// ErrNoPhases reports a Cycle with nothing to run.
	ErrNoPhases = errors.New("cycle: has no phases")
	// ErrNoExit reports a phase with no exit Condition, which would advance
	// on the model's own account of itself.
	ErrNoExit = errors.New("cycle: phase has no exit condition")
	// ErrUnknownCondition reports a configured exit condition name that no
	// built-in condition answers to.
	ErrUnknownCondition = errors.New("cycle: unknown exit condition")
	// ErrUnknownCycle reports a cycle name the project does not define.
	ErrUnknownCycle = errors.New("cycle: unknown cycle")
)

// Phase is one stage of a Cycle: what the model is told to achieve, the
// tools it may use, and the Condition that decides whether the Cycle moves
// on. Tool narrowing is a routing lever rather than a fence: a narrow phase
// is a job a small local model can drive, and a model that cannot edit
// source can still reach a green check through a mock or a hardcoded
// return, so the exit Condition is what has to be hard to cheat.
type Phase struct {
	Exit        condition.Condition[State]
	Name        string
	Goal        string
	Tools       []string
	MaxAttempts int
	// Gated runs this phase's Loop against the project's verifier, so the
	// change set has to pass the gates before the Loop reports complete. A
	// phase whose product is a failing test leaves it off, since verification
	// rounds would otherwise be spent undoing the artifact the phase was
	// asked to produce.
	Gated bool
}

// Cycle is a named list of phases run in order.
type Cycle struct {
	Name   string
	Phases []Phase
}

// Validate reports a Cycle that cannot be run: no phases, or a phase with no
// exit Condition. It is checked at load rather than mid-run, because a phase
// with no Condition would silently advance on a claim.
func (c Cycle) Validate() error {
	if len(c.Phases) == 0 {
		return fmt.Errorf("%w: %s", ErrNoPhases, c.Name)
	}

	for _, p := range c.Phases {
		if p.Exit == nil {
			return fmt.Errorf("%w: %s/%s", ErrNoExit, c.Name, p.Name)
		}
	}

	return nil
}

// attempts reports the phase's bound, filled in from the default when the
// definition left it at zero.
func (p Phase) attempts() int {
	if p.MaxAttempts <= 0 {
		return DefaultMaxAttempts
	}

	return p.MaxAttempts
}

// State is what a phase's exit Condition reads: the standing goal, the
// change set accumulated across the Cycle so far, the rows its phases
// recorded, and how the phase's Loop ended. It carries nothing the model
// said about its own progress.
type State struct {
	RepoRoot string
	Goal     string
	Phase    string
	// LoopComplete reports whether the phase's Loop ended on its own
	// completion condition, which for a gated phase means the gates passed.
	LoopReason   string
	Changes      []tool.Change
	Ledger       Rows
	LoopComplete bool
}

// mergeChanges accumulates a phase's changes onto the Cycle's set, keeping
// one entry per path with the last line ranges recorded for it.
func mergeChanges(into, from []tool.Change) []tool.Change {
	out := append([]tool.Change{}, into...)

	for _, c := range from {
		replaced := false

		for i := range out {
			if out[i].Path == c.Path {
				out[i] = c
				replaced = true

				break
			}
		}

		if !replaced {
			out = append(out, c)
		}
	}

	return out
}
