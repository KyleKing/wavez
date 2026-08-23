package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/condition"
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
)

// stubCycles hands the manager one cycle whose single phase never satisfies
// its Condition, so the thread's end state is what a harness refusal looks
// like from a client.
type stubCycles struct{ holds bool }

func (s stubCycles) Cycle(name string) (cycle.Cycle, error) {
	if name != "stub" {
		return cycle.Cycle{}, cycle.ErrUnknownCycle
	}

	exit := condition.Func("stub-exit", func(context.Context, cycle.State) (condition.Verdict, error) {
		if s.holds {
			return condition.Met("stub-exit", "held"), nil
		}

		return condition.Unmet("stub-exit", "the artifact never failed"), nil
	})

	return cycle.Cycle{Name: "stub", Phases: []cycle.Phase{{Name: "only", Exit: exit, MaxAttempts: 1}}}, nil
}

//nolint:ireturn // the manager consumes this as cycle.Driver
func (stubCycles) CycleDriver(thread.ID, []string, router.Input) cycle.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Drive(context.Context, cycle.Attempt) (cycle.PhaseResult, error) {
	return cycle.PhaseResult{Complete: true, Turns: 1}, nil
}

// A cycle thread ends done only when every phase's Condition held, and
// failed otherwise, with the verdicts on its own log. A cycle the daemon
// does not know is refused at creation rather than run as an ordinary turn.
func TestCycleThreadEndsOnItsCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  event.State
		holds bool
	}{
		{name: "condition holds", holds: true, want: event.StateDone},
		{name: "condition unmet", holds: false, want: event.StateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})
			m.cycles = stubCycles{holds: tt.holds}

			mt, err := m.create(createParams{Prompt: "fix it", Cycle: "stub", Dirs: []string{t.TempDir()}})
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			if err := m.send(mt.id, "fix it", false); err != nil {
				t.Fatalf("send: %v", err)
			}

			m.waitIdle(t.Context())

			events, err := mt.th.Log().Since(0)
			if err != nil {
				t.Fatalf("reading log: %v", err)
			}

			got := summarizeCycleLog(events)
			if got.last != tt.want {
				t.Errorf("final state = %s, want %s", got.last, tt.want)
			}

			if got.verdicts != 1 {
				t.Errorf("logged %d verdict(s), want 1", got.verdicts)
			}

			if phase, ok := phaseOf(events[1]); !ok || phase != "only" {
				t.Errorf("first cycle event names phase %q (%v), want only", phase, ok)
			}
		})
	}
}

// cycleLogSummary is the last state a cycle thread reached and how many
// verdicts it logged on the way.
type cycleLogSummary struct {
	last     event.State
	verdicts int
}

func summarizeCycleLog(events []event.Event) cycleLogSummary {
	var out cycleLogSummary

	for i := range events {
		if events[i].Kind == event.KindState {
			out.last = events[i].State
		}

		if events[i].Kind == event.KindCycle && events[i].Detail["event"] == "verdict" {
			out.verdicts++
		}
	}

	return out
}

func TestCreateRefusesAnUnknownCycle(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	if _, err := m.create(createParams{Cycle: "fix", Dirs: []string{t.TempDir()}}); !errors.Is(err, ErrNoCycles) {
		t.Errorf("without a source: err = %v, want ErrNoCycles", err)
	}

	m.cycles = stubCycles{}
	_, err := m.create(createParams{Cycle: "nope", Dirs: []string{t.TempDir()}})
	if !errors.Is(err, cycle.ErrUnknownCycle) {
		t.Errorf("unknown cycle: err = %v, want ErrUnknownCycle", err)
	}
}
