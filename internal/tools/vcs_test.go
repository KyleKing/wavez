package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

type fakeRepo struct {
	diff string
	log  string
}

func (f fakeRepo) WorkingCopyDiff(context.Context, string) (string, error) { return f.diff, nil }
func (f fakeRepo) Log(context.Context, string, int) (string, error)        { return f.log, nil }

type fakeChanges struct{ changed []tool.Change }

func (f fakeChanges) Changed() []tool.Change { return f.changed }

// Version control reaches a run through this and never through the shell,
// so what matters most is what the tool cannot do: there is no verb that
// commits, pushes, or rewrites, and asking for one is refused rather than
// passed to a CLI.
func TestVCS_ReadsAndRefusesEverythingElse(t *testing.T) {
	t.Parallel()

	repo := fakeRepo{
		diff: "diff --git a/a.go b/a.go\n@@\n-x\n+y\ndiff --git a/b.go b/b.go\n@@\n-p\n+q\n",
		log:  "abc 2026-08-24 feat: something\n",
	}
	changes := fakeChanges{changed: []tool.Change{{Path: "a.go", Added: 2, Removed: 1}}}
	v := tools.NewVCS(t.TempDir(), repo, changes)

	for _, tc := range []struct {
		name    string
		input   map[string]any
		want    string
		wantErr bool
	}{
		{
			name:  "status reads what this run changed",
			input: map[string]any{"operation": "status"}, want: "a.go +2 -1",
		},
		{
			name:  "diff reads the working copy",
			input: map[string]any{"operation": "diff"}, want: "b.go",
		},
		{
			name:  "diff narrows to one file",
			input: map[string]any{"operation": "diff", "path": "a.go"}, want: "a.go",
		},
		{
			name:  "log reads history",
			input: map[string]any{"operation": "log"}, want: "feat: something",
		},
		{
			name:  "there is no verb that writes",
			input: map[string]any{"operation": "commit"}, want: "writes nothing", wantErr: true,
		},
		{
			name:  "nor one that pushes",
			input: map[string]any{"operation": "push"}, want: "writes nothing", wantErr: true,
		},
		{
			name:  "an absent operation names the three that exist",
			input: map[string]any{}, want: "operation is required", wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := v.Run(context.Background(), mustJSON(t, tc.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.IsError != tc.wantErr {
				t.Errorf("IsError = %v, want %v (%s)", result.IsError, tc.wantErr, result.Content)
			}

			if !strings.Contains(result.Content, tc.want) {
				t.Errorf("Content = %q, want it to contain %q", result.Content, tc.want)
			}
		})
	}
}

// A diff narrowed to one file must not carry the others, which is the whole
// reason the path exists: the working copy of a run several edits deep is
// larger than the window it has left.
func TestVCS_ANarrowedDiffDropsTheOtherFiles(t *testing.T) {
	t.Parallel()

	repo := fakeRepo{diff: "diff --git a/a.go b/a.go\n-x\ndiff --git a/b.go b/b.go\n-p\n"}
	v := tools.NewVCS(t.TempDir(), repo, fakeChanges{})

	result, err := v.Run(context.Background(), mustJSON(t, map[string]any{
		"operation": "diff", "path": "b.go",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(result.Content, "a.go") {
		t.Errorf("Content = %q, want the other file's section dropped", result.Content)
	}
}
