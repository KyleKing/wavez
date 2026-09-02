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

// A formatter rewrites the files the other gates are about to read, so it
// takes the worktree exclusively the way the built-in Go formatter does:
// without that, lint and the tests read a file while it is being rewritten,
// which is what all 25 retractions over an unchanged tree came from.
func TestCommandGate_ARewritingCheckTakesTheWorktree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := map[string]struct {
		want     string
		rewrites bool
	}{
		"a formatter": {want: gate.WorktreeResource, rewrites: true},
		"a reporter":  {want: "check:format", rewrites: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gates := gate.NewCommandGates(root, []gate.CommandCheck{
				{Name: "format", Command: "true", Paths: []string{"*.py"}, Rewrites: tt.rewrites},
			})

			if len(gates) != 1 {
				t.Fatalf("built %d gates, want 1", len(gates))
			}

			if got := gates[0].Resources(); len(got) != 1 || got[0] != tt.want {
				t.Errorf("Resources = %v, want [%s]", got, tt.want)
			}
		})
	}
}

// What a monorepo needs of a project check: run in the stack's own
// directory, over the files the run actually changed, so one repository can
// declare a formatter per stack and neither reformats the other's tree.
func TestCommandGate_RunsInItsDirectoryOverTheChangedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps", "api"), 0o750); err != nil {
		t.Fatalf("creating the stack: %v", err)
	}

	for _, rel := range []string{"apps/api/main.py", "apps/api/other.py", "apps/web/app.ts"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", rel, err)
		}

		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}

	gates := gate.NewCommandGates(root, []gate.CommandCheck{{
		Name:    "format:api",
		Dir:     "apps/api",
		Paths:   []string{"apps/api/**/*.py"},
		Command: "pwd > ran.txt; echo {files} >> ran.txt",
	}})

	if len(gates) != 1 {
		t.Fatalf("built %d gates, want 1", len(gates))
	}

	res, err := gates[0].Run(t.Context(), gate.RunContext{
		Changes: []tool.Change{{Path: "apps/api/main.py"}, {Path: "apps/web/app.ts"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.Pass {
		t.Fatalf("Result = %+v, want a pass", res)
	}

	//nolint:gosec // a file this test's own command wrote inside its temp dir
	out, err := os.ReadFile(filepath.Join(root, "apps", "api", "ran.txt"))
	if err != nil {
		t.Fatalf("the command did not run in its own directory: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		t.Fatalf("ran.txt = %q, want the directory and the files", out)
	}

	if !strings.HasSuffix(lines[0], filepath.Join("apps", "api")) {
		t.Errorf("ran in %q, want apps/api", lines[0])
	}

	// The web file changed too and this check does not name it.
	if lines[1] != "main.py" {
		t.Errorf("files = %q, want the one changed file it names, relative to its directory", lines[1])
	}
}
