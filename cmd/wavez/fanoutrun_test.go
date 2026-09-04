package main

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/fanout"
)

// A lane pasted into a shell and a lane run by -fanout-run have to be the
// same task, or the printed plan stops describing what running it does. The
// two used to build the sentence separately.
func TestPrintedPlanAndRunLaneShareOnePrompt(t *testing.T) {
	t.Parallel()

	lane := fanout.Lane{Paths: []string{"src/a.py", "src/b.py"}, Weight: 3}
	prompt := lanePrompt("ruff check .", lane)

	out := laneReport("ruff check .", []fanout.Task{{Name: "src/a.py", Weight: 3}}, []fanout.Lane{lane})

	if !strings.Contains(out, shellQuote(prompt)) {
		t.Fatalf("the printed plan does not carry the prompt a run would use:\n%s", out)
	}
}

// Several lanes open within one call, and the generated id is a nanosecond
// clock reading, so a coarse clock gives two threads one event log.
func TestLaneThreadIDsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for i := range 8 {
		id := string(laneThreadID(i))
		if seen[id] {
			t.Fatalf("lane %d reused thread id %s", i, id)
		}

		seen[id] = true
	}
}

// A lane whose changes failed verification stops with a nil error, so a
// report reading the error alone called it done while its change set had
// been abandoned. That happened on the first three-lane run against
// vcr-tui: lane 1 wrote a `type` alias the project's Python cannot parse,
// failed verification twice, and printed "done".
func TestLaneStatusNamesABoundThatEndedTheRunWithoutAnError(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		want   string
		result laneResult
	}{
		"a completed lane": {
			result: laneResult{outcome: agent.Outcome{Turns: 5, Stop: agent.StopComplete}},
			want:   "done",
		},
		"verification failed and the changes were abandoned": {
			result: laneResult{outcome: agent.Outcome{Turns: 20, Stop: agent.StopVerifyFailed}},
			want:   "stopped: verify_failed",
		},
		"the turn bound tripped": {
			result: laneResult{outcome: agent.Outcome{Turns: 30, Stop: agent.StopMaxTurns}},
			want:   "stopped: max_turns",
		},
		"complete but a finish bound objected": {
			result: laneResult{outcome: agent.Outcome{
				Turns: 8, Stop: agent.StopComplete, FinishFindings: []string{"names a file it never opened"},
			}},
			want: "finished with names a file it never opened",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := laneStatus(&tc.result); got != tc.want {
				t.Errorf("laneStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
