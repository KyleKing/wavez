package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

var errGoimportsMissing = errors.New("goimports not found on PATH")

// stubGate is an in-memory gate.Gate: GateVerifier's construction is
// injectable specifically so its ordering and short-circuit behavior can
// be tested without shelling out to a real toolchain.
type stubGate struct {
	err    error
	seen   *[]gate.RunContext
	name   string
	result gate.Result
}

func (g *stubGate) Name() string      { return g.name }
func (*stubGate) Resources() []string { return nil }

func (g *stubGate) Run(_ context.Context, rc gate.RunContext) (gate.Result, error) {
	if g.seen != nil {
		*g.seen = append(*g.seen, rc)
	}

	return g.result, g.err
}

type gateVerifierCase struct {
	name                 string
	gates                []gate.Gate
	wantFeedbackContains string
	wantRanGates         []string
	wantOK               bool
}

func TestGateVerifier_Verify(t *testing.T) {
	t.Parallel()

	tests := []gateVerifierCase{
		{
			name: "every gate passes",
			gates: []gate.Gate{
				&stubGate{name: "format", result: gate.Result{Gate: "format", Pass: true}},
				&stubGate{name: "build", result: gate.Result{Gate: "build", Pass: true}},
				&stubGate{name: "go-test", result: gate.Result{Gate: "go-test", Pass: true}},
			},
			wantOK:       true,
			wantRanGates: []string{"format", "build", "go-test"},
		},
		{
			name: "a build failure stops before tests run",
			gates: []gate.Gate{
				&stubGate{name: "format", result: gate.Result{Gate: "format", Pass: true}},
				&stubGate{name: "build", result: gate.Result{
					Gate:     "build",
					Failures: []gate.TrimmedFailure{{Test: "build", Frames: []string{"a.go:3: undefined: filepath"}}},
				}},
				&stubGate{name: "go-test", result: gate.Result{Gate: "go-test", Pass: true}},
			},
			wantOK:               false,
			wantRanGates:         []string{"format", "build"},
			wantFeedbackContains: "undefined: filepath",
		},
		{
			name: "a failure whose frames were trimmed away still says which gate and that it could not say more",
			gates: []gate.Gate{
				&stubGate{name: "go-test", result: gate.Result{
					Gate:     "go-test",
					Failures: []gate.TrimmedFailure{{Package: "."}},
				}},
			},
			wantOK:               false,
			wantRanGates:         []string{"go-test"},
			wantFeedbackContains: "go-test build .",
		},
		{
			name: "a gate error becomes failing feedback instead of silently passing",
			gates: []gate.Gate{
				&stubGate{name: "format", err: errGoimportsMissing},
				&stubGate{name: "build", result: gate.Result{Gate: "build", Pass: true}},
			},
			wantOK:               false,
			wantRanGates:         []string{"format"},
			wantFeedbackContains: errGoimportsMissing.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log, err := gate.OpenLog(filepath.Join(t.TempDir(), "gate.log"))
			if err != nil {
				t.Fatalf("OpenLog: %v", err)
			}

			v := app.NewGateVerifier("/repo", nil, nil, log, gate.RealClock{}, tt.gates, nil, nil)

			feedback, verdict := v.Verify(context.Background(), []tool.Change{{Path: "a.go"}})
			assertVerifyOutcome(t, log, feedback, verdict == agent.VerdictPass, tt)
		})
	}
}

// TestGateVerifier_PartitionByRepo covers how a run's changes are split by
// the repository they belong to. Only the changes under repoRoot reach the
// gates. Work under a declared extra directory is recorded as an abstention
// naming the directory, never fed back as a failure the run could act on,
// and a run whose changes are entirely outside the root runs no gate at all.
func TestGateVerifier_PartitionByRepo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		wantVerdict   agent.GateVerdict
		wantUncovered string
		changes       []tool.Change
		wantRanGates  []string
		wantSeenPaths []string
	}{
		{
			name:          "all changes in root, gates run as today",
			changes:       []tool.Change{{Path: "internal/app/verifier.go"}, {Path: "internal/gate/gate.go"}},
			wantRanGates:  []string{"format", "build"},
			wantSeenPaths: []string{"internal/app/verifier.go", "internal/gate/gate.go"},
			wantVerdict:   agent.VerdictPass,
		},
		{
			name: "all changes outside root, no gate runs and the directory is named",
			changes: []tool.Change{
				{Path: "/extra/sub/p1.go"},
				{Path: "/extra/sub/p2.go"},
			},
			wantRanGates:  []string{"scope"},
			wantVerdict:   agent.VerdictPass,
			wantUncovered: "/extra/sub",
		},
		{
			name: "a mix gates only the in-root subset",
			changes: []tool.Change{
				{Path: "internal/app/verifier.go"},
				{Path: "/other/tree/file.go"},
			},
			wantRanGates:  []string{"scope", "format", "build"},
			wantSeenPaths: []string{"internal/app/verifier.go"},
			wantVerdict:   agent.VerdictPass,
			wantUncovered: "/other/tree",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log, err := gate.OpenLog(filepath.Join(t.TempDir(), "gate.log"))
			if err != nil {
				t.Fatalf("OpenLog: %v", err)
			}

			var seen []gate.RunContext
			recorder := &stubGate{name: "build", result: gate.Result{Gate: "build", Pass: true}, seen: &seen}

			v := app.NewGateVerifier("/repo", nil, nil, log, gate.RealClock{},
				[]gate.Gate{
					&stubGate{name: "format", result: gate.Result{Gate: "format", Pass: true}},
					recorder,
				}, nil, nil)

			feedback, verdict := v.Verify(context.Background(), tt.changes)
			if verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q", verdict, tt.wantVerdict)
			}
			assertFeedback(t, feedback, "")
			assertRanGates(t, log, tt.wantRanGates)
			assertUncovered(t, log, tt.wantUncovered)
			assertSeenPaths(t, seen, tt.wantSeenPaths)
		})
	}
}

func assertFeedback(t *testing.T, feedback, wantSubstring string) {
	t.Helper()

	if wantSubstring != "" {
		if !strings.Contains(feedback, wantSubstring) {
			t.Errorf("feedback = %q, want substring %q", feedback, wantSubstring)
		}

		return
	}

	if feedback != "" {
		t.Errorf("feedback = %q, want empty", feedback)
	}
}

// assertUncovered checks that the abstention names the directory this
// project's gates did not cover, which is the only record a run editing
// another repository leaves.
func assertUncovered(t *testing.T, log *gate.Log, want string) {
	t.Helper()

	entries, err := log.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	var reason string

	for i := range entries {
		if entries[i].Gate == "scope" {
			reason = entries[i].Reason
		}
	}

	if want == "" {
		if reason != "" {
			t.Errorf("uncovered reason = %q, want none recorded", reason)
		}

		return
	}

	if !strings.Contains(reason, want) {
		t.Errorf("uncovered reason = %q, want substring %q", reason, want)
	}

	if !strings.Contains(reason, "outside this project") {
		t.Errorf("uncovered reason = %q, want it named as out of scope", reason)
	}
}

func assertRanGates(t *testing.T, log *gate.Log, want []string) {
	t.Helper()

	entries, err := log.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	ran := make([]string, len(entries))
	for i := range entries {
		ran[i] = entries[i].Gate
	}

	if !slices.Equal(ran, want) {
		t.Errorf("logged gates = %v, want %v", ran, want)
	}
}

func assertSeenPaths(t *testing.T, seen []gate.RunContext, want []string) {
	t.Helper()

	var paths []string
	for _, rc := range seen {
		for _, ch := range rc.Changes {
			paths = append(paths, ch.Path)
		}
	}

	if !slices.Equal(paths, want) {
		t.Errorf("gates saw changes %v, want %v", paths, want)
	}
}

func assertVerifyOutcome(t *testing.T, log *gate.Log, feedback string, ok bool, tt gateVerifierCase) {
	t.Helper()

	if ok != tt.wantOK {
		t.Errorf("ok = %v, want %v", ok, tt.wantOK)
	}
	if tt.wantOK && feedback != "" {
		t.Errorf("feedback = %q, want empty on pass", feedback)
	}
	if tt.wantFeedbackContains != "" && !strings.Contains(feedback, tt.wantFeedbackContains) {
		t.Errorf("feedback = %q, want substring %q", feedback, tt.wantFeedbackContains)
	}

	entries, err := log.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	ranGates := make([]string, len(entries))
	for i := range entries {
		ranGates[i] = entries[i].Gate
	}

	if !reflect.DeepEqual(ranGates, tt.wantRanGates) {
		t.Errorf("logged gates = %v, want %v", ranGates, tt.wantRanGates)
	}
}

// stubWriters scripts what the lease manager reports about other lanes.
type stubWriters struct{ others []string }

func (w stubWriters) OtherActiveHolders(_, _ string) []string { return w.others }

// TestGateVerifier_AttributedAdvisory covers the half of the parallel-lane
// decision that decides what a run is told about a line it did not write. A
// finding somebody else left never fails the gate, it is named as theirs when
// this run is alone in the tree, and it stays out entirely while another lane
// is still writing, since it will have moved before the run could act.
func TestGateVerifier_AttributedAdvisory(t *testing.T) {
	t.Parallel()

	const neighbor = "internal/daemon/daemon.go:369:3: ineffectual assignment to ln"

	failing := gate.Result{
		Gate: "lint",
		Failures: []gate.TrimmedFailure{
			{Test: "lint", Frames: []string{"internal/runtime/server.go:12:2: unused variable"}},
		},
		Advisories: []gate.TrimmedFailure{
			{Test: "lint", Writer: "another writer", Frames: []string{neighbor}},
		},
	}

	tests := []struct {
		writers app.Writers
		name    string
		want    bool
	}{
		{name: "alone in the tree, the neighbor's finding is named", writers: stubWriters{}, want: true},
		{
			name:    "another lane writing, the neighbor's finding stays out",
			writers: stubWriters{others: []string{"other-thread"}},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log, err := gate.OpenLog(filepath.Join(t.TempDir(), "gate.log"))
			if err != nil {
				t.Fatalf("OpenLog: %v", err)
			}

			v := app.NewGateVerifier("/repo", nil, nil, log, gate.RealClock{},
				[]gate.Gate{&stubGate{name: "lint", result: failing}}, nil, tt.writers)

			feedback, verdict := v.Verify(context.Background(), []tool.Change{{Path: "internal/runtime/server.go"}})
			if verdict != agent.VerdictFailed {
				t.Fatalf("verdict = %q, want failed: an advisory must not change the verdict", verdict)
			}
			if !strings.Contains(feedback, "unused variable") {
				t.Fatalf("feedback dropped the run's own finding: %q", feedback)
			}
			if got := strings.Contains(feedback, neighbor); got != tt.want {
				t.Errorf("feedback names the neighbor = %v, want %v: %q", got, tt.want, feedback)
			}
			if tt.want && !strings.Contains(feedback, "another writer") {
				t.Errorf("feedback did not attribute the finding: %q", feedback)
			}
		})
	}
}
