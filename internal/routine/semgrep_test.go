package routine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/routine"
)

const cleanJSON = `{"results": [], "paths": {"scanned": ["main.go"]}}`

const findingsJSON = `{"results": [
  {"check_id": "go.lang.security.audit.exec.Command",
   "path": "cmd/run.go", "start": {"line": 12},
   "extra": {"message": "exec.Command with user input"}}
], "paths": {"scanned": ["cmd/run.go"]}}`

// errAbsentBinary is the LookPath result for a checkout without semgrep.
var errAbsentBinary = errors.New("executable file not found")

// fakeSemgrepCommand returns a runCmd that replays json and, when findings
// are present, semgrep's exit status for "found something".
func fakeSemgrepCommand(json string, findings bool) func(context.Context, string, []string, string) ([]byte, error) {
	return func(context.Context, string, []string, string) ([]byte, error) {
		if findings {
			return []byte(json), &fakeExitError{code: 1}
		}

		return []byte(json), nil
	}
}

type fakeExitError struct {
	code int
}

func (fakeExitError) Error() string { return "exit status 1" }

// fakeExitError satisfies the same errors.As target exec's ExitError does;
// the scanner checks exit codes through that interface.
func (fakeExitError) ExitCode() int { return 1 }

// runSemgrepRoutine compiles the built-in set with the semgrep action, opts
// in or out the way ".wavez.pkl" would, and runs the routine.
func runSemgrepRoutine(
	t *testing.T,
	action routine.Action,
	optIn bool,
) routine.RunRecord {
	t.Helper()

	reg := routine.NewRegistry(action)
	defs := map[string]routine.Definition{}
	if optIn {
		defs[routine.SemgrepName] = routine.Definition{Enabled: true}
	}

	set, err := routine.CompileSet(defs, reg, "hash")
	require.NoError(t, err, "compiling the routine set")

	rt, ok := set.Get(routine.SemgrepName)
	require.True(t, ok, "the set has no semgrep routine")

	runner := routine.NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil)
	rec, err := runner.Run(context.Background(), rt, routine.TriggerManual, routine.Env{Root: t.TempDir()})
	if errors.Is(err, routine.ErrDisabled) {
		// The opted-out built-in refuses the run outright, which is what a
		// disabled routine does.
		return routine.RunRecord{Routine: routine.SemgrepName}
	}

	require.NoError(t, err, "running the routine")

	return rec
}

func TestSemgrepRoutine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		action     routine.Action
		name       string
		wantStatus routine.Status
		wantDetail string
		optIn      bool
	}{
		{
			name:  "opted out leaves it disabled",
			optIn: false,
			action: routine.SemgrepAction("", routine.WithSemgrepLookPath(func(string) (string, error) {
				return "/bin/semgrep", nil
			}), routine.WithSemgrepCommand(fakeSemgrepCommand(cleanJSON, false))),
			wantStatus: "",
		},
		{
			name:  "opted in with no binary abstains",
			optIn: true,
			action: routine.SemgrepAction("", routine.WithSemgrepLookPath(func(string) (string, error) {
				return "", errAbsentBinary
			})),
			// The step reports pass with nothing examined, which the runner
			// marks as an abstention.
			wantStatus: routine.StatusAbstained,
		},
		{
			name:  "opted in with findings fails naming the file and line",
			optIn: true,
			action: routine.SemgrepAction("", routine.WithSemgrepLookPath(func(string) (string, error) {
				return "/bin/semgrep", nil
			}), routine.WithSemgrepCommand(fakeSemgrepCommand(findingsJSON, true))),
			wantStatus: routine.StatusFail,
			wantDetail: "cmd/run.go:12",
		},
		{
			name:  "opted in clean passes",
			optIn: true,
			action: routine.SemgrepAction("", routine.WithSemgrepLookPath(func(string) (string, error) {
				return "/bin/semgrep", nil
			}), routine.WithSemgrepCommand(fakeSemgrepCommand(cleanJSON, false))),
			wantStatus: routine.StatusPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := runSemgrepRoutine(t, tt.action, tt.optIn)

			if !tt.optIn {
				assert.False(t, rec.Pass, "a disabled routine does not pass")
				assert.Empty(t, rec.Steps, "a disabled routine runs no steps")

				return
			}

			require.Len(t, rec.Steps, 1, "the semgrep routine has one step")
			assert.Equal(t, tt.wantStatus, rec.Steps[0].Status)

			if tt.wantDetail != "" {
				require.NotEmpty(t, rec.Steps[0].Failures, "findings should be reported")
				assert.Contains(t, rec.Steps[0].Failures[0].Frames[0], tt.wantDetail)
			}
		})
	}
}
