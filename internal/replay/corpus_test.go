package replay_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/replay"
)

// The corpus report exists because one run's counters read as noise, and
// the two things it must not get wrong are what counts as a failure and how
// much of the breakdown is missing. `delete`'s 60% error rate was a third
// once its correct refusals came out.
func TestCorpusSeparatesRefusalsAndNamesWhatItCannotClassify(t *testing.T) {
	t.Parallel()

	recs := []replay.Record{
		{
			Run:    replay.Run{Task: "h4"},
			Checks: []replay.CheckResult{{Check: "a", Pass: true}},
			Stats: bench.Stats{Turns: 4, Tools: []bench.ToolStat{{
				Name: "delete", Calls: 10, Errors: 6, Refusals: 4,
				Causes: map[string]int{"refused": 4, "no_match": 1},
			}}},
			Complete: true,
		},
		{
			Run:    replay.Run{Task: "h4"},
			Checks: []replay.CheckResult{{Check: "a", Pass: false}},
			Stats:  bench.Stats{Turns: 8},
		},
	}

	var out strings.Builder
	if err := replay.Corpus(recs, &out); err != nil {
		t.Fatalf("Corpus: %v", err)
	}

	got := out.String()

	want := []string{
		"2 runs, 1/2 (50%) ended complete",
		// Six errors less four refusals, and a cause map covering five of
		// the six.
		"2/10 (20%)",
		"unclassified 1",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("report missing %q:\n%s", w, got)
		}
	}
}
