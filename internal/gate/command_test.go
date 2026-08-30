package gate_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// A project in another language reaches the model with nothing behind its
// edits, because every gate wavez ships speaks Go. A declared check is how it
// gets the same change-triggered loop.
func TestCommandGateRunsWhatTheProjectDeclared(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("def f():\n    return 1\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	tests := map[string]struct {
		changed  string
		command  string
		wantPass bool
		wantRan  bool
		wantAttr bool
	}{
		"a failure naming the changed file is attributed to it": {
			changed:  "app.py",
			command:  `echo "app.py:2:5: E999 broken"; exit 1`,
			wantRan:  true,
			wantAttr: true,
		},
		"a failure naming nothing the run changed is not": {
			changed: "app.py",
			command: `echo "other.py:1:1: E999 broken"; exit 1`,
			wantRan: true,
		},
		"a nested path matches by base name": {
			changed:  "a/b/c/thing.py",
			command:  `echo "a/b/c/thing.py:1:1: E999 broken"; exit 1`,
			wantRan:  true,
			wantAttr: true,
		},
		"a path the check does not name": {
			changed:  "README.md",
			command:  `exit 1`,
			wantPass: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gates := gate.NewCommandGates(root, []gate.CommandCheck{
				{Name: "ruff", Paths: []string{"*.py"}, Command: tt.command},
			})
			if len(gates) != 1 {
				t.Fatalf("NewCommandGates built %d gates, want 1", len(gates))
			}

			result, err := gates[0].Run(t.Context(), gate.RunContext{
				Selection: gate.Selection{Level: gate.LevelPackage},
				Changes:   []tool.Change{{Path: tt.changed, Added: 1}},
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v: %+v", result.Pass, tt.wantPass, result)
			}

			if ran := result.Examined > 0; ran != tt.wantRan {
				t.Fatalf("Examined = %d, want the check to have run = %v", result.Examined, tt.wantRan)
			}

			if tt.wantRan {
				assertFailure(t, result, tt.wantAttr)
			}
		})
	}
}

// assertFailure checks the one failure a run of the check produced, and
// whether it was attributed to a file the run changed. A frame is a line
// naming such a file, which is what tells a failure the run caused from one
// it inherited.
func assertFailure(t *testing.T, result gate.Result, wantAttributed bool) {
	t.Helper()

	if len(result.Failures) != 1 {
		t.Fatalf("Failures = %+v, want exactly one", result.Failures)
	}

	lines := slices.Concat(result.Failures[0].Frames, result.Failures[0].Context)
	if !strings.Contains(strings.Join(lines, "\n"), "E999") {
		t.Errorf("Failures = %+v, want the command's own output", result.Failures)
	}

	if attributed := len(result.Failures[0].Frames) > 0; attributed != wantAttributed {
		t.Errorf("Frames = %q, Context = %q, want attributed = %v",
			result.Failures[0].Frames, result.Failures[0].Context, wantAttributed)
	}
}

// A check with no command is not a check, and building a gate for it would
// run `sh -c ""` on every change and pass.
func TestCommandGatesSkipAnIncompleteCheck(t *testing.T) {
	t.Parallel()

	gates := gate.NewCommandGates(t.TempDir(), []gate.CommandCheck{
		{Name: "no-command"},
		{Command: "true"},
	})
	if len(gates) != 0 {
		t.Fatalf("NewCommandGates built %d gates, want none", len(gates))
	}
}
