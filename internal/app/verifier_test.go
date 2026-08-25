package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

			v := app.NewGateVerifier("/repo", nil, nil, log, gate.RealClock{}, tt.gates, nil)

			feedback, ok := v.Verify(context.Background(), []tool.Change{{Path: "a.go"}})
			assertVerifyOutcome(t, log, feedback, ok, tt)
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
