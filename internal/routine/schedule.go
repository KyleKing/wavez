package routine

import (
	"context"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// MinInterval is the shortest cadence a scheduled routine may declare, and
// Compile refuses one below it. A routine that runs the test suite every
// second is a laptop with no CPU left for the run that asked for it, and a
// typo in seconds is how that happens.
const MinInterval = 30 * time.Second

// Timekeeper starts a scheduled routine when its interval has elapsed.
//
// Each routine keeps its own next-due time rather than sharing one tick, so
// a nightly audit and a five-minute check do not drag each other's cadence,
// and a run that overruns its interval delays only itself: the next due time
// is set from when the run finished, so a routine never queues behind
// itself.
//
// A routine with no interval never fires, because a schedule trigger with no
// cadence names no time to run at.
type Timekeeper struct {
	clock  gate.Clock
	runner *Runner
	set    func() *Set
	root   string
	every  time.Duration
}

// NewTimekeeper builds a Timekeeper that wakes every `every` to see what is
// due. The wake interval bounds how late a routine can start, so it wants to
// be well under the shortest cadence a project declares.
func NewTimekeeper(root string, runner *Runner, set func() *Set, every time.Duration) *Timekeeper {
	return &Timekeeper{root: root, runner: runner, set: set, every: every, clock: gate.RealClock{}}
}

// Run fires due routines until ctx ends. It returns only when ctx does.
func (t *Timekeeper) Run(ctx context.Context) {
	due := map[string]time.Time{}

	timer := t.clock.NewTimer(t.every)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
			t.fireDue(ctx, due)
			timer.Reset(t.every)
		}
	}
}

// fireDue runs every scheduled routine whose interval has elapsed, one at a
// time: they share the machine, and a project that declared four of them on
// the same cadence did not ask for four suites at once.
func (t *Timekeeper) fireDue(ctx context.Context, due map[string]time.Time) {
	env := Env{Root: t.root, Selection: gate.Selection{Level: gate.LevelPackage}}

	for _, rt := range t.set().Triggered(TriggerSchedule) {
		now := t.clock.Now()

		next, seen := due[rt.Name]
		if !seen {
			// Coming up is not a schedule tick, so an hourly routine runs an
			// hour from now rather than on every daemon restart.
			due[rt.Name] = now.Add(rt.Interval)

			continue
		}

		if now.Before(next) {
			continue
		}

		//nolint:errcheck // a routine's outcome is its history entry, which is where a reader looks
		_, _ = t.runner.Run(ctx, rt, TriggerSchedule, env)

		// From when the run finished, so a routine never queues behind itself.
		due[rt.Name] = t.clock.Now().Add(rt.Interval)
	}
}
