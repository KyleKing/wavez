package main

import (
	"strings"
	"testing"

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
