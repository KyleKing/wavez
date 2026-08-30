package routine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// stoppedClock is gate.Clock with a time a test sets, since what the due
// logic answers is a question about wall time and nothing else.
type stoppedClock struct{ now time.Time }

func (c *stoppedClock) Now() time.Time { return c.now }

//nolint:ireturn // gate.Clock's contract is the Timer interface
func (*stoppedClock) NewTimer(d time.Duration) gate.Timer { return gate.RealClock{}.NewTimer(d) }

// A schedule trigger is a cadence, so the three answers that matter are
// "not yet", "now", and "not at startup": a daemon restart must not fire
// every scheduled routine.
func TestTimekeeper_FiresOnlyWhatIsDue(t *testing.T) {
	t.Parallel()

	var runs atomic.Int64

	reg := NewRegistry(Action{
		Name: "count",
		Bind: func(map[string]any) (Bound, error) {
			return Bound{Run: func(context.Context, Env) (Outcome, error) {
				runs.Add(1)

				return Outcome{Pass: true, Examined: 1}, nil
			}}, nil
		},
	})

	rt, err := Compile(Definition{
		Name:     "hourly",
		Triggers: []Trigger{TriggerSchedule},
		Interval: time.Hour,
		Steps:    []StepDef{{Name: "count", Action: "count"}},
		Enabled:  true,
	}, reg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	set := &Set{byName: map[string]*Routine{rt.Name: rt}}
	clock := &stoppedClock{now: time.Unix(0, 0)}
	tk := &Timekeeper{
		root: t.TempDir(), runner: NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil),
		set: func() *Set { return set }, every: time.Second, clock: clock,
	}

	due := map[string]time.Time{}

	tk.fireDue(context.Background(), due)
	if got := runs.Load(); got != 0 {
		t.Fatalf("ran %d times at startup, want 0", got)
	}

	clock.now = clock.now.Add(59 * time.Minute)
	tk.fireDue(context.Background(), due)
	if got := runs.Load(); got != 0 {
		t.Fatalf("ran %d times before the interval elapsed, want 0", got)
	}

	clock.now = clock.now.Add(2 * time.Minute)
	tk.fireDue(context.Background(), due)
	if got := runs.Load(); got != 1 {
		t.Fatalf("ran %d times once due, want 1", got)
	}

	tk.fireDue(context.Background(), due)
	if got := runs.Load(); got != 1 {
		t.Fatalf("ran %d times, want the next run to wait another interval", got)
	}
}

// A cadence below the floor is a typo in seconds, and a routine that runs
// the suite every second leaves no machine for the run that asked for it.
func TestCompile_RefusesAScheduleBelowTheFloor(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(Action{
		Name: "noop",
		Bind: func(map[string]any) (Bound, error) {
			return Bound{Run: func(context.Context, Env) (Outcome, error) {
				return Outcome{Pass: true, Examined: 1}, nil
			}}, nil
		},
	})

	def := Definition{
		Name:     "too-often",
		Triggers: []Trigger{TriggerSchedule},
		Interval: time.Second,
		Steps:    []StepDef{{Name: "noop", Action: "noop"}},
		Enabled:  true,
	}

	if _, err := Compile(def, reg); err == nil {
		t.Fatal("Compile accepted a one-second schedule, want a refusal")
	}

	def.Interval = MinInterval
	if _, err := Compile(def, reg); err != nil {
		t.Fatalf("Compile at the floor: %v", err)
	}
}
