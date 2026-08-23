package bench

import "github.com/kyleking/wavez/internal/event"

// Attribution is what a run's turns went to. Productive and Retrieval are
// exact from the log: a turn either produced an accepted change or it did
// not. Harness is an estimate and is marked as one, because "this turn
// exists only because the harness said something" is a judgment about
// cause, and the log records what happened rather than why.
type Attribution struct {
	// Productive turns produced at least one accepted change.
	Productive int `json:"productive"`
	// Retrieval turns called tools and changed nothing.
	Retrieval int `json:"retrieval"`
	// Harness turns followed a tool error or something the harness injected
	// (gate feedback, a nudge), so the run spent them reacting to the
	// harness rather than to the task. It wins over the other classes
	// wherever both apply, because the question it answers is how much of
	// the run the harness caused.
	Harness int `json:"harness"`
	// Prose turns called no tool at all.
	Prose int `json:"prose"`
}

// Total is every turn attributed.
func (a Attribution) Total() int { return a.Productive + a.Retrieval + a.Harness + a.Prose }

// turnState accumulates one turn's shape as its events go by.
type turnState struct {
	open      bool
	reacting  bool
	changed   bool
	failed    bool
	toolCalls int
}

// Attribute classifies every turn in a thread's log.
//
// A turn opens at the marker AppendAssistant writes and runs until the next
// one, so its tool results belong to it. Harness wins over the others
// wherever both apply, since the question it answers is how much of the run
// the harness caused.
func Attribute(evs []event.Event) Attribution {
	var (
		out      Attribution
		turn     turnState
		injected bool
	)

	for i := range evs {
		ev := &evs[i]

		switch ev.Kind {
		case event.KindAgent:
			if ev.Role == "" {
				continue
			}

			injected = injected || turn.failed
			out.close(&turn)
			turn = turnState{open: true, reacting: injected}
			injected = false
		case event.KindTool:
			turn.toolCalls++
			turn.changed = turn.changed || len(ev.Changes) > 0
			turn.failed = turn.failed || boolField(ev.Detail, "is_error")
		case event.KindUser:
			// Only the first prompt precedes any turn; everything after it
			// is the harness talking, since a user cannot reach a working
			// thread mid-run.
			injected = turn.open
		case event.KindGate, event.KindPermission, event.KindState, event.KindError, event.KindLedger,
			event.KindUsage, event.KindCycle, event.KindHypothesis, event.KindGoal, event.KindReview:
		default:
		}
	}

	out.close(&turn)

	return out
}

func (a *Attribution) close(t *turnState) {
	switch {
	case !t.open:
	case t.reacting:
		a.Harness++
	case t.changed:
		a.Productive++
	case t.toolCalls > 0:
		a.Retrieval++
	default:
		a.Prose++
	}
}
