package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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
	name   string
	result gate.Result
}

func (g *stubGate) Name() string      { return g.name }
func (*stubGate) Resources() []string { return nil }

func (g *stubGate) Run(context.Context, gate.RunContext) (gate.Result, error) {
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
