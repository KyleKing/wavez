// Package condition carries the check that decides when a run may stop or
// advance. A Loop's stop reasons and a Cycle phase's exit gate are the same
// idea at two granularities, so both report a Verdict: whether the check
// holds, and the reason a client renders beside it. The harness evaluates a
// Condition; a model never reports one.
package condition

import (
	"context"
	"fmt"
)

// Verdict is one Condition's answer about one state. Reason is written for a
// reader, so it names what was examined rather than restating Holds.
type Verdict struct {
	Condition string `json:"condition"`
	Reason    string `json:"reason"`
	Holds     bool   `json:"holds"`
}

// Met builds a Verdict for a check that holds.
func Met(name, reason string) Verdict {
	return Verdict{Condition: name, Reason: reason, Holds: true}
}

// Unmet builds a Verdict for a check that does not hold. The reason is what
// the caller reports instead of advancing, so it must say what is missing.
func Unmet(name, reason string) Verdict {
	return Verdict{Condition: name, Reason: reason, Holds: false}
}

// Condition decides whether a run over state S may stop or advance. An error
// means the check could not run, which is never the same as it not holding:
// a caller must report the failure rather than treat it as a refusal.
type Condition[S any] interface {
	Name() string
	Holds(ctx context.Context, state S) (Verdict, error)
}

// funcCondition adapts a function to Condition.
type funcCondition[S any] struct {
	fn   func(context.Context, S) (Verdict, error)
	name string
}

func (c funcCondition[S]) Name() string { return c.name }

func (c funcCondition[S]) Holds(ctx context.Context, state S) (Verdict, error) {
	return c.fn(ctx, state)
}

// Func builds a Condition from a function.
func Func[S any](name string, fn func(context.Context, S) (Verdict, error)) Condition[S] {
	return funcCondition[S]{name: name, fn: fn}
}

// All holds only when every part holds, reporting the first part that does
// not. It short-circuits, so an expensive check can be ordered behind a cheap
// one.
func All[S any](name string, parts ...Condition[S]) Condition[S] {
	return Func(name, func(ctx context.Context, state S) (Verdict, error) {
		for _, part := range parts {
			verdict, err := part.Holds(ctx, state)
			if err != nil {
				return Verdict{}, fmt.Errorf("evaluating %s: %w", part.Name(), err)
			}

			if !verdict.Holds {
				return Unmet(name, verdict.Reason), nil
			}
		}

		return Met(name, "every part held"), nil
	})
}
