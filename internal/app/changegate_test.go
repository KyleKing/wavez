package app_test

import (
	"fmt"
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
			want: []string{"go-test TestTTL", "lease.go:12: want 30s", "Fix the cause"},
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
			want: []string{
				"go-test build internal/guard", "is not in std",
				"Decide whether the change caused it",
			},
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

			g := app.NewChangeGate(nil, gate.NewRunScope())
			g.Collect(gate.RunResult{Gates: tt.results})

			got, _ := g.TakeFeedback("")

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

			if again, _ := g.TakeFeedback(""); again != "" {
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

	g := app.NewChangeGate(nil, gate.NewRunScope())
	g.Collect(gate.RunResult{Gates: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
		Package: "internal/thread", Frames: []string{"thread.go:31:9: undefined: utf8"},
	}}}}})

	g.TakeFeedback("")

	status, ok := g.Status("")
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

	g := app.NewChangeGate(nil, gate.NewRunScope())

	g.Collect(gate.RunResult{Gates: []gate.Result{
		{Gate: "go-test", Failures: []gate.TrimmedFailure{{Package: "internal/guard"}}},
		{Gate: "format", Pass: true, Examined: 1},
	}})

	if alarms := g.FalseAlarms(""); len(alarms) != 0 {
		t.Fatalf("FalseAlarms() = %v on the first verdict, want none", alarms)
	}

	g.Collect(gate.RunResult{Gates: []gate.Result{{Gate: "go-test", Pass: true, Examined: 1}}})

	alarms := g.FalseAlarms("")
	if len(alarms) != 1 || alarms[0] != "go-test" {
		t.Fatalf("FalseAlarms() = %v, want [go-test]", alarms)
	}

	if again := g.FalseAlarms(""); len(again) != 0 {
		t.Errorf("FalseAlarms() = %v on a second call, want it cleared", again)
	}
}

// An edit between the two verdicts explains the change of answer, so the
// gate was right both times and this must stay silent.
func TestChangeGateKeepsQuietWhenAnEditExplainsThePass(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil, gate.NewRunScope())
	g.Collect(gate.RunResult{Gates: []gate.Result{
		{Gate: "go-test", Failures: []gate.TrimmedFailure{{Package: "internal/guard"}}},
	}})

	g.Enqueue(tool.Change{Path: "internal/guard/guard.go", Added: 1})
	g.Collect(gate.RunResult{Gates: []gate.Result{{Gate: "go-test", Pass: true, Examined: 1}}})

	if alarms := g.FalseAlarms(""); len(alarms) != 0 {
		t.Errorf("FalseAlarms() = %v, want none: an edit landed between the two verdicts", alarms)
	}
}

// Every other escalation this loop makes reads one turn, which catches a
// tier that cannot emit a call and never one that emits fine and cannot
// solve the problem. On `e2` the fast tier spends five runs in six on the
// same compile error, quoted back every round, and escalates only when the
// deadline does it for it.
func TestChangeGateNamesATierThatCannotMoveAFailure(t *testing.T) {
	t.Parallel()

	failing := []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{
		{Package: "internal/sysinfo", Frames: []string{"memory_test.go:12: undefined: Memory"}},
	}}}

	t.Run("the same failure across edits", func(t *testing.T) {
		t.Parallel()

		g := app.NewChangeGate(nil, gate.NewRunScope())

		for i := range 3 {
			g.Enqueue(tool.Change{Path: "internal/sysinfo/memory_test.go", Added: 1})
			g.Collect(gate.RunResult{Gates: failing})

			if name, stuck := g.Stuck(""); stuck != (i == 2) {
				t.Fatalf("after %d rounds Stuck() = %q, %v, want stuck=%v", i+1, name, stuck, i == 2)
			}
		}

		if name, _ := g.Stuck(""); name != "go-test" {
			t.Errorf("Stuck() named %q, want the gate that failed", name)
		}
	})

	t.Run("a re-run over the same tree is not the tier's fault", func(t *testing.T) {
		t.Parallel()

		g := app.NewChangeGate(nil, gate.NewRunScope())
		for range 4 {
			g.Collect(gate.RunResult{Gates: failing})
		}

		if name, stuck := g.Stuck(""); stuck {
			t.Errorf("Stuck() = %q, %v after four debounced re-runs of one change, want false", name, stuck)
		}
	})

	// Gate batches are debounced, so one turn's edits can arrive as two
	// results. A run that edits, is told the same thing, edits again, and is
	// told it twice more reached three edits against one failure, and the
	// re-run in the middle is not evidence it started converging.
	t.Run("a re-run between edits does not clear the count", func(t *testing.T) {
		t.Parallel()

		g := app.NewChangeGate(nil, gate.NewRunScope())

		for range 3 {
			g.Enqueue(tool.Change{Path: "internal/sysinfo/memory_test.go", Added: 1})
			g.Collect(gate.RunResult{Gates: failing})
			g.Collect(gate.RunResult{Gates: failing})
		}

		if name, stuck := g.Stuck(""); !stuck {
			t.Errorf("Stuck() = %q, %v after three edits against one failure, want true", name, stuck)
		}
	})

	t.Run("a failure that moved is progress", func(t *testing.T) {
		t.Parallel()

		g := app.NewChangeGate(nil, gate.NewRunScope())

		for i := range 4 {
			g.Enqueue(tool.Change{Path: "internal/sysinfo/memory_test.go", Added: 1})
			g.Collect(gate.RunResult{Gates: []gate.Result{{
				Gate: "go-test",
				Failures: []gate.TrimmedFailure{{
					Package: "internal/sysinfo",
					Frames:  []string{fmt.Sprintf("memory_test.go:%d: undefined: Memory", i)},
				}},
			}}})
		}

		if name, stuck := g.Stuck(""); stuck {
			t.Errorf("Stuck() = %q, %v, want false: the failure moved every round", name, stuck)
		}
	})
}

// The shell answers a scoped sweep from the gates only where they ran, so
// the package the run never wrote in has to come back false however many
// others it did write in.
func TestChangeGateCoversOnlyThePackagesItWroteIn(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil, gate.NewRunScope())
	g.Enqueue(tool.Change{Path: "internal/bench/stats.go", Added: 1})
	g.Enqueue(tool.Change{Path: "internal/bench/testdata/x.golden", Added: 1})

	tests := map[string]struct {
		pkgs []string
		want bool
	}{
		"the package it changed":     {pkgs: []string{"internal/bench"}, want: true},
		"one it did not":             {pkgs: []string{"cmd/wavez"}, want: false},
		"both together":              {pkgs: []string{"internal/bench", "cmd/wavez"}, want: false},
		"a package named by no file": {pkgs: []string{"internal/bench/testdata"}, want: false},
		"nothing at all":             {pkgs: nil, want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := g.Covers("", tt.pkgs); got != tt.want {
				t.Errorf("Covers(%q) = %v, want %v", tt.pkgs, got, tt.want)
			}
		})
	}
}

// One agent.Loop serves every thread, so one ChangeGate's results used to be
// one set: two lanes ran side by side, and the gate told the lane working on
// the go-test gate that the other lane's brand-new file did not compile,
// under "Gates ran on your changes". That lane went and edited the other's
// file and died on the result. A writer's feedback is its own now.
func TestChangeGate_OneWritersFindingsNeverReachAnother(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil, gate.NewRunScope())

	g.Collect(gate.RunResult{
		Changes: []tool.Change{{Path: "internal/runtime/cacheram.go", Writer: "lane-b"}},
		Gates: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{
			{Test: "build", Frames: []string{"internal/runtime/cacheram.go:81:13: undefined: osStat"}},
		}}},
	})

	if got, failed := g.TakeFeedback("lane-a"); got != "" || failed {
		t.Errorf("lane-a was told %q about lane-b's changes", got)
	}

	got, failed := g.TakeFeedback("lane-b")
	if !failed || !strings.Contains(got, "undefined: osStat") {
		t.Errorf("lane-b was not told about its own failure: %q", got)
	}
}

// The shell reaches Changed, Status and Covers with no thread of its own, so
// those three used to flatten every writer: a lane asking what it had changed
// was told about the lane beside it. The thread asking arrives on the tool
// call's context now, and the same three answers narrow with it. A caller
// naming no writer still gets every writer's answer, which is what a replay
// fixture or a one-off probe expects.
func TestChangeGate_TreeStateAnswersNarrowToTheWriter(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil, gate.NewRunScope())

	g.Enqueue(tool.Change{Path: "internal/runtime/a.go", Added: 1, Writer: "lane-a"})
	g.Enqueue(tool.Change{Path: "cmd/wavez/b.go", Added: 1, Writer: "lane-b"})
	g.Collect(gate.RunResult{
		Changes: []tool.Change{{Path: "internal/runtime/a.go", Writer: "lane-a"}},
		Gates:   []gate.Result{{Gate: "go-test", Pass: true, Examined: 1}},
	})

	t.Run("Changed lists one writer's files and not the other's", func(t *testing.T) {
		t.Parallel()

		if got := g.Changed("lane-a"); len(got) != 1 || got[0].Path != "internal/runtime/a.go" {
			t.Errorf("lane-a Changed() = %v, want only internal/runtime/a.go", got)
		}

		if got := g.Changed("lane-b"); len(got) != 1 || got[0].Path != "cmd/wavez/b.go" {
			t.Errorf("lane-b Changed() = %v, want only cmd/wavez/b.go", got)
		}

		if got := g.Changed(""); len(got) != 2 {
			t.Errorf("no-writer Changed() = %v, want both writers' files", got)
		}
	})

	t.Run("Status answers for the writer that asked", func(t *testing.T) {
		t.Parallel()

		laneAFailureReachesOnlyLaneB(t, g)
	})

	t.Run("Covers counts only the writer's packages", func(t *testing.T) {
		t.Parallel()

		if !g.Covers("lane-a", []string{"internal/runtime"}) {
			t.Error("lane-a Covers(internal/runtime) = false, want the package it changed")
		}

		if g.Covers("lane-a", []string{"cmd/wavez"}) {
			t.Error("lane-a Covers(cmd/wavez) = true, want lane-b's package to stay lane-b's")
		}

		if g.Covers("lane-b", []string{"internal/runtime"}) {
			t.Error("lane-b Covers(internal/runtime) = true, want lane-a's package to stay lane-a's")
		}

		if !g.Covers("", []string{"cmd/wavez"}) {
			t.Error("no-writer Covers(cmd/wavez) = false, want the every-writer answer")
		}
	})
}

// laneAFailureReachesOnlyLaneB gives lane-b a failing gate and checks that
// Status reports it to lane-b alone, while a caller naming no writer still
// hears something.
func laneAFailureReachesOnlyLaneB(t *testing.T, g *app.ChangeGate) {
	t.Helper()

	g.Collect(gate.RunResult{
		Changes: []tool.Change{{Path: "cmd/wavez/b.go", Writer: "lane-b"}},
		Gates: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{
			{Test: "build", Frames: []string{"cmd/wavez/b.go:9:2: undefined: flagParse"}},
		}}},
	})
	g.TakeFeedback("lane-b")

	status, _ := g.Status("lane-a")
	if strings.Contains(status, "flagParse") {
		t.Errorf("lane-a Status() = %q, want lane-b's failure to stay lane-b's", status)
	}

	status, ok := g.Status("lane-b")
	if !ok || !strings.Contains(status, "flagParse") {
		t.Errorf("lane-b Status() = %q, %v, want its own failure", status, ok)
	}

	if _, ok = g.Status(""); !ok {
		t.Error("no-writer Status() said nothing, want the every-writer answer")
	}
}

// Begin clears the writer it names and nothing else, since a lane starting
// beside a lane still working must not take that lane's report with it.
func TestChangeGate_BeginClearsOnlyItsOwnWriter(t *testing.T) {
	t.Parallel()

	g := app.NewChangeGate(nil, gate.NewRunScope())

	g.Collect(gate.RunResult{
		Changes: []tool.Change{{Path: "a.go", Writer: "lane-a"}},
		Gates:   []gate.Result{{Gate: "lint", Failures: []gate.TrimmedFailure{{Test: "lint"}}}},
	})

	g.Begin("lane-b")

	if got, _ := g.TakeFeedback("lane-a"); got == "" {
		t.Error("lane-b starting cleared lane-a's pending report")
	}
}
