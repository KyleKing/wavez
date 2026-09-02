package finish_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/finish"
)

// The bound is weak on purpose: touching the file the task names proves
// nothing about whether the edit was right, and touching none of them
// proves the run did not do the task as written.
func TestChangeSetMatchesTask(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "internal/thread/thread.go", "package thread\n\nfunc truncate() {}\n")
	write(t, root, "internal/edit/edit.go", "package edit\n")

	tests := []struct {
		name    string
		task    string
		changed []string
		wantOK  bool
	}{
		{
			name:    "the named file was written",
			task:    "In internal/thread/thread.go rewrite truncate.",
			changed: []string{"internal/thread/thread.go"},
			wantOK:  true,
		},
		{
			name:    "a path named by its tail matches the file under it",
			task:    "Fix the ty diagnostic at thread/thread.go:17.",
			changed: []string{"internal/thread/thread.go"},
			wantOK:  true,
		},
		{
			name:    "a run that wrote somewhere else is named",
			task:    "In internal/thread/thread.go rewrite truncate.",
			changed: []string{"internal/edit/edit.go"},
		},
		{
			name:    "a task naming only a symbol is satisfied by a file that holds it",
			task:    "Rewrite `truncate` so it cuts on a character boundary.",
			changed: []string{"internal/thread/thread.go"},
			wantOK:  true,
		},
		{
			name:    "a task that names nothing checkable abstains",
			task:    "Make the tests faster.",
			changed: []string{"internal/edit/edit.go"},
			wantOK:  true,
		},
		{
			name:   "a run that changed nothing fails a task that names a file",
			task:   "In internal/thread/thread.go rewrite truncate.",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report, err := finish.ChangeSetMatchesTask(root, tt.task, tt.changed)
			if err != nil {
				t.Fatalf("ChangeSetMatchesTask: %v", err)
			}

			if report.OK() != tt.wantOK {
				t.Fatalf("OK() = %v, want %v:\n%s", report.OK(), tt.wantOK, report)
			}
		})
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()

	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// The goal check is the harness observation the roadmap parks a
// model-authored goal behind: it says the run has not touched what the goal
// names and never says the goal is wrong.
func TestChangeSetMatchesGoalNamesTheGoalItChecked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "a.go", "package a\n")

	report, err := finish.ChangeSetMatchesGoal(root, "Fix internal/lease/lease.go", []string{"a.go"})
	if err != nil {
		t.Fatalf("ChangeSetMatchesGoal: %v", err)
	}

	if report.OK() {
		t.Fatal("a run that wrote a.go passed a goal naming internal/lease/lease.go")
	}

	if !strings.Contains(report.String(), "goal") {
		t.Errorf("report = %q, want it to say which of the two bounds failed", report)
	}
}
