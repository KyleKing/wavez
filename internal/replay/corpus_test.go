package replay_test

import (
	"strings"
	"testing"
	"time"

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

// Twelve lanes shipped these three measurements and nothing had read any of
// them over the corpus, which is the only scale at which they say anything:
// where a run's turns go, whether the gates retract their own failures, and
// how often a run completed with every gate green having done something of
// the wrong shape.
func TestCorpusReadsTheMeasurementsOneRunCannotShow(t *testing.T) {
	t.Parallel()

	recs := []replay.Record{
		{
			Run: replay.Run{Task: "e2"}, Started: "2026-08-23T10:00:00Z",
			Stats: bench.Stats{
				TurnsBy:         bench.Attribution{Productive: 1, Retrieval: 2, Harness: 5},
				GateRounds:      4,
				GateFailures:    3,
				GateFalseAlarms: 1,
				FinishFindings:  map[string]int{"substance": 1},
			},
		},
		{
			Run: replay.Run{Task: "e2"}, Started: "2026-08-24T10:00:00Z",
			Stats: bench.Stats{TurnsBy: bench.Attribution{Prose: 2}, GateRounds: 1},
		},
	}

	var out strings.Builder
	if err := replay.Corpus(recs, &out); err != nil {
		t.Fatalf("Corpus: %v", err)
	}

	got := out.String()

	want := []string{
		"10 turns attributed",
		"harness 5/10 (50%)",
		"5 gate rounds, 3/5 (60%) failed, 1 retracted",
		"1/2 (50%) of runs finished with a finding",
		"substance",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("report missing %q:\n%s", w, got)
		}
	}
}

// A rate over the whole corpus averages every harness this project has had.
// The `str_replace` failures recorded before its schema stated a top-level
// oneOf count a hole a later run cannot fall into, and 111 of the errors
// predate the taxonomy entirely, so they report as unclassified and blunt
// every rate they sit in.
func TestSinceDropsTheRunsFromAnEarlierHarness(t *testing.T) {
	t.Parallel()

	recs := []replay.Record{
		{Run: replay.Run{Task: "e2"}, Started: "2026-08-21T10:00:00Z"},
		{Run: replay.Run{Task: "e2"}, Started: "2026-08-23T10:00:00Z"},
		{Run: replay.Run{Task: "e2"}, Started: "not a timestamp"},
	}

	kept := replay.Since(recs, time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC))
	if len(kept) != 2 {
		t.Fatalf("kept %d records, want the one on the boundary and the unparsable one: %+v", len(kept), kept)
	}

	if kept[0].Started != "2026-08-23T10:00:00Z" {
		t.Errorf("kept[0].Started = %q, want the run on the boundary", kept[0].Started)
	}
}
