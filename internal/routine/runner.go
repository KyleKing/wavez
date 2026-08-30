package routine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kyleking/wavez/internal/gate"
)

// Runner executes compiled routines: steps in dependency order, independent
// steps concurrently, steps sharing a resource key serialized on the
// process-wide gate.ResourceSet, and one run per concurrency key at a time.
// A Runner is safe for concurrent use.
type Runner struct {
	clock   gate.Clock
	res     *gate.ResourceSet
	history *History
	groups  *groups
}

// NewRunner builds a Runner over the resource set the gates already share,
// so a routine step running `go test` waits on a gate run doing the same
// rather than racing it. Both res and history may be nil.
func NewRunner(clock gate.Clock, res *gate.ResourceSet, history *History) *Runner {
	return &Runner{clock: clock, res: res, history: history, groups: newGroups()}
}

// Run executes rt and records the run in the history. It returns the record
// even when a step failed: a failing check is a result, not an error. The
// error is non-nil only when the run could not happen at all.
func (r *Runner) Run(ctx context.Context, rt *Routine, trigger Trigger, env Env) (RunRecord, error) {
	if !rt.Enabled {
		return RunRecord{}, fmt.Errorf("routine %q: %w", rt.Name, ErrDisabled)
	}

	runCtx, release, err := r.groups.acquire(ctx, rt)
	if err != nil {
		return RunRecord{}, fmt.Errorf("waiting for concurrency key %q: %w", rt.key(), err)
	}
	defer release()

	rec := r.execute(runCtx, rt, trigger, env)

	if err := r.history.Append(rec); err != nil {
		return rec, err
	}

	return rec, nil
}

func (r *Runner) execute(ctx context.Context, rt *Routine, trigger Trigger, env Env) RunRecord {
	start := r.clock.Now()
	records := make([]StepRecord, len(rt.Steps))
	failed := make(map[string]bool, len(rt.Steps))

	for _, wave := range rt.Order {
		var wg sync.WaitGroup

		for _, i := range wave {
			step := rt.Steps[i]
			if parentFailed(step, failed) {
				records[i] = StepRecord{Name: step.Name, Action: step.Action, Status: StatusSkipped}

				continue
			}

			wg.Add(1)

			go func() {
				defer wg.Done()

				records[i] = r.runStep(ctx, step, env)
			}()
		}

		wg.Wait()

		for _, i := range wave {
			// An abstention does not block what depends on it: the step
			// found nothing to check, which is not the same as finding a
			// problem, and stopping the run there would hide the steps that
			// do have something to say.
			if records[i].Status != StatusPass && records[i].Status != StatusAbstained {
				failed[rt.Steps[i].Name] = true
			}
		}
	}

	return RunRecord{
		Timestamp: start,
		Routine:   rt.Name,
		Trigger:   trigger,
		Steps:     records,
		Duration:  r.clock.Now().Sub(start),
		Pass:      allPassed(records),
	}
}

func (r *Runner) runStep(ctx context.Context, step Step, env Env) StepRecord {
	start := r.clock.Now()
	rec := StepRecord{Name: step.Name, Action: step.Action}

	releaseResources := r.res.Lock(step.bound.Resources)
	outcome, err := step.bound.Run(ctx, env)

	releaseResources()

	rec.Duration = r.clock.Now().Sub(start)

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		rec.Status = StatusCanceled
	case err != nil:
		rec.Status = StatusError
		rec.Error = err.Error()
	case outcome.Pass && outcome.Examined == 0:
		rec.Status = StatusAbstained
	case outcome.Pass:
		rec.Status = StatusPass
		rec.Examined = outcome.Examined
	default:
		rec.Status = StatusFail
		rec.Examined = outcome.Examined
		rec.Failures = outcome.Failures
	}

	return rec
}

func parentFailed(step Step, failed map[string]bool) bool {
	for _, p := range step.Parents {
		if failed[p] {
			return true
		}
	}

	return false
}

// allPassed reports a run that checked something and found nothing wrong.
// An abstained step neither passes nor fails it, but a run where every step
// abstained checked nothing and is not a pass.
func allPassed(records []StepRecord) bool {
	passed := false

	for _, rec := range records {
		switch rec.Status {
		case StatusPass:
			passed = true
		case StatusAbstained:
		case StatusFail, StatusSkipped, StatusCanceled, StatusError:
			return false
		}
	}

	return passed
}
