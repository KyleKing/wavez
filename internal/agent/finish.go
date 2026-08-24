package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// Finish is what a Finisher is given: the task as the user stated it, the
// thread's standing goal, the run's closing prose, and what it wrote.
type Finish struct {
	Task string
	Goal string
	// Answer is the prose of the turn that ended the run.
	Answer string
	// Checkpoint is the run's starting operation id, so a check that needs
	// what the run actually wrote can diff the whole run rather than guess
	// from the paths.
	Checkpoint string
	Changes    []tool.Change
}

// Finisher answers deterministically whether a finished run did something
// of the right shape. It replaces the model reviewer's question with bounds
// a model plays no part in: the reviewer objected to correct diffs in 3 of
// 77 recorded runs and never turned a failure into a success.
//
// It returns findings rather than a verdict, and never an objection the
// model is asked to argue with, because each finding is a fact about the
// run rather than an opinion about it.
type Finisher interface {
	Check(ctx context.Context, f Finish) ([]string, error)
}

// WithFinisher configures Run to bound a completing run by the
// deterministic finish checks.
func WithFinisher(f Finisher) Option { return func(o *Options) { o.Finisher = f } }

// runFinishChecks records what the checks found. Findings never fail the
// run and never reach the model: they are the harness's account of a run
// that already ended, and handing them back would make them the reviewer
// they exist to replace.
func (r *run) runFinishChecks(ctx context.Context) error {
	if r.loop.options.Finisher == nil {
		return nil
	}

	findings, err := r.loop.options.Finisher.Check(ctx, Finish{
		Task: r.task, Goal: r.thread.Goal(), Answer: r.answer,
		Checkpoint: r.outcome.Checkpoint, Changes: r.changes,
	})
	if err != nil {
		return fmt.Errorf("running the finish checks: %w", err)
	}

	if len(findings) == 0 {
		return nil
	}

	r.outcome.FinishFindings = findings

	if _, err := r.thread.Log().Append(event.Event{
		Kind:   event.KindFinish,
		Text:   "finish checks: " + strings.Join(findings, "; "),
		Detail: map[string]any{"findings": findings},
	}); err != nil {
		return fmt.Errorf("logging the finish checks: %w", err)
	}

	return nil
}
