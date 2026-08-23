package app_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// A passing gate says nothing and a failing one always says something. The
// second half is the one that bit: a build failure whose frames did not
// survive trimming reached the model as a bare gate name, and it spent 26
// turns guessing what had failed.
func TestChangeGateFeedback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		results  []gate.Result
		want     []string
		wantNone bool
	}{
		{
			name:    "a pass names the gate, so the run does not check it again through the shell",
			results: []gate.Result{{Gate: "go-test", Pass: true, Examined: 3}},
			want:    []string{"passed: go-test", "Do not re-run"},
		},
		{
			name:     "a gate that examined nothing has abstained and says nothing",
			results:  []gate.Result{{Gate: "go-test", Pass: true}},
			wantNone: true,
		},
		{
			name: "a named test failure carries its frames",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
				Test: "TestTTL", Package: "lease", Frames: []string{"lease.go:12: want 30s"},
			}}}},
			want: []string{"go-test TestTTL", "lease.go:12: want 30s"},
		},
		{
			name: "a build failure has no test name and is named by package",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
				Package: "tmp/calc", Frames: []string{"calc.go:5:9: cannot use \"sum\""},
			}}}},
			want: []string{"go-test build tmp/calc", "cannot use"},
		},
		{
			name: "a failure that names no changed file carries what the command printed",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
				Package: "internal/guard",
				Context: []string{"package internal/guard is not in std (/usr/local/go/src/internal/guard)"},
			}}}},
			want: []string{"go-test build internal/guard", "is not in std"},
		},
		{
			name:    "a failure the gate could not describe still names the gate",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{}}}},
			want:    []string{"go-test failed without reporting which check"},
		},
		{
			name:    "a failing gate with no failures at all still says so",
			results: []gate.Result{{Gate: "lsp"}},
			want:    []string{"lsp failed without reporting which check"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := app.NewChangeGate(nil)
			g.Collect(gate.RunResult{Gates: tt.results})

			got := g.TakeFeedback()

			if tt.wantNone {
				if got != "" {
					t.Errorf("TakeFeedback() = %q, want empty; an abstention is not news", got)
				}

				return
			}

			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("TakeFeedback() = %q, want it to contain %q", got, want)
				}
			}

			if g.TakeFeedback() != "" {
				t.Error("TakeFeedback() did not clear; the same failure would repeat every turn")
			}
		})
	}
}

// A run whose gates failed asks the shell whether the build is fixed, and
// being told it was already answered leaves it nowhere: feedback is
// delivered once. One `h6` run spent six turns on that.
func TestChangeGateStatusRepeatsWhatFailed(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil)
	g.Collect(gate.RunResult{Gates: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
		Package: "internal/thread", Frames: []string{"thread.go:31:9: undefined: utf8"},
	}}}}})

	g.TakeFeedback()

	status, ok := g.Status()
	if !ok {
		t.Fatal("Status() said nothing about a failure it holds")
	}

	for _, want := range []string{"failed", "go-test build internal/thread", "undefined: utf8"} {
		if !strings.Contains(status, want) {
			t.Errorf("Status() = %q, want it to contain %q", status, want)
		}
	}
}

// A gate that fails and then passes over the same change set was wrong the
// first time, and nothing about the code moved between the two answers.
// `h5` was exactly that, and naming it took three re-runs.
func TestChangeGateNamesAGateThatRetractedItsFailure(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil)

	g.Collect(gate.RunResult{Gates: []gate.Result{
		{Gate: "go-test", Failures: []gate.TrimmedFailure{{Package: "internal/guard"}}},
		{Gate: "format", Pass: true, Examined: 1},
	}})

	if alarms := g.FalseAlarms(); len(alarms) != 0 {
		t.Fatalf("FalseAlarms() = %v on the first verdict, want none", alarms)
	}

	g.Collect(gate.RunResult{Gates: []gate.Result{{Gate: "go-test", Pass: true, Examined: 1}}})

	alarms := g.FalseAlarms()
	if len(alarms) != 1 || alarms[0] != "go-test" {
		t.Fatalf("FalseAlarms() = %v, want [go-test]", alarms)
	}

	if again := g.FalseAlarms(); len(again) != 0 {
		t.Errorf("FalseAlarms() = %v on a second call, want it cleared", again)
	}
}

// An edit between the two verdicts explains the change of answer, so the
// gate was right both times and this must stay silent.
func TestChangeGateKeepsQuietWhenAnEditExplainsThePass(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil)
	g.Collect(gate.RunResult{Gates: []gate.Result{
		{Gate: "go-test", Failures: []gate.TrimmedFailure{{Package: "internal/guard"}}},
	}})

	g.Enqueue(tool.Change{Path: "internal/guard/guard.go", Added: 1})
	g.Collect(gate.RunResult{Gates: []gate.Result{{Gate: "go-test", Pass: true, Examined: 1}}})

	if alarms := g.FalseAlarms(); len(alarms) != 0 {
		t.Errorf("FalseAlarms() = %v, want none: an edit landed between the two verdicts", alarms)
	}
}
