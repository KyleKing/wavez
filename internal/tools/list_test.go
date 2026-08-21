package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

func TestList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{
		"main.go",
		"internal/tools/list.go",
		"internal/tools/list_test.go",
		"docs/notes.md",
		".git/objects/blob",
		"node_modules/pkg/index.js",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	tests := []struct {
		name    string
		dir     string
		pattern string
		want    []string
		absent  []string
	}{
		{
			name:   "the whole tree skips the directories no model asks for",
			want:   []string{"main.go", "internal/tools/list.go", "docs/notes.md"},
			absent: []string{".git/objects/blob", "node_modules/pkg/index.js"},
		},
		{
			name:    "a bare glob matches the file name at any depth",
			pattern: "*.go",
			want:    []string{"main.go", "internal/tools/list.go"},
			absent:  []string{"docs/notes.md"},
		},
		{
			name:    "a glob holding a slash matches the path",
			pattern: "internal/*/*_test.go",
			want:    []string{"internal/tools/list_test.go"},
			absent:  []string{"internal/tools/list.go"},
		},
		{
			name:    "a miss says so rather than returning an empty listing",
			pattern: "*.rs",
			want:    []string{"no files matching"},
		},
		{
			name:   "dir scopes the walk",
			dir:    "docs",
			want:   []string{"notes.md"},
			absent: []string{"main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := tools.NewList(root)
			result, err := l.Run(context.Background(), mustJSON(t, map[string]any{
				"dir": tt.dir, "pattern": tt.pattern,
			}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.IsError {
				t.Fatalf("IsError = true: %q", result.Content)
			}
			checkListing(t, result.Content, tt.want, tt.absent)
		})
	}
}

func TestList_RefusesPathOutsideRoot(t *testing.T) {
	t.Parallel()

	l := tools.NewList(t.TempDir())
	result, err := l.Run(context.Background(), mustJSON(t, map[string]any{"dir": "../.."}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "outside the project root") {
		t.Errorf("Content = %q (IsError=%v), want a refusal", result.Content, result.IsError)
	}
}

func checkListing(t *testing.T, content string, want, absent []string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(content, w) {
			t.Errorf("Content = %q, want it to hold %q", content, w)
		}
	}
	for _, a := range absent {
		if strings.Contains(content, a) {
			t.Errorf("Content = %q, want it to omit %q", content, a)
		}
	}
}
