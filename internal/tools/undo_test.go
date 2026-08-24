package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

func TestUndoRestoresOnlyWhatTheRunWrote(t *testing.T) {
	t.Parallel()

	const original = "package p\n\nfunc F() int { return 1 }\n"

	cases := []struct {
		name    string
		edit    func(t *testing.T, root string, scope *tools.Scope)
		path    string
		wantErr string
		want    string
		gone    bool
	}{
		{
			name: "puts an edited file back",
			edit: func(t *testing.T, root string, scope *tools.Scope) {
				t.Helper()
				abs := filepath.Join(root, "a.go")
				if err := scope.Edit(abs); err != nil {
					t.Fatalf("Edit: %v", err)
				}
				write(t, abs, "package p\n\nfunc F() int { return 2 }\n")
			},
			path: "a.go",
			want: original,
		},
		{
			name: "puts back what the run first found, not its previous edit",
			edit: func(t *testing.T, root string, scope *tools.Scope) {
				t.Helper()
				abs := filepath.Join(root, "a.go")
				for _, body := range []string{"two", "three"} {
					if err := scope.Edit(abs); err != nil {
						t.Fatalf("Edit: %v", err)
					}
					write(t, abs, "package p // "+body+"\n")
				}
			},
			path: "a.go",
			want: original,
		},
		{
			name: "removes a file the run created",
			edit: func(t *testing.T, root string, scope *tools.Scope) {
				t.Helper()
				abs := filepath.Join(root, "new.go")
				if err := scope.Edit(abs); err != nil {
					t.Fatalf("Edit: %v", err)
				}
				write(t, abs, "package p\n")
			},
			path: "new.go",
			gone: true,
		},
		{
			name:    "refuses a file the run never edited",
			edit:    func(*testing.T, string, *tools.Scope) {},
			path:    "a.go",
			wantErr: "has not edited",
		},
		{
			name:    "refuses a path outside the project",
			edit:    func(*testing.T, string, *tools.Scope) {},
			path:    "../escape.go",
			wantErr: "outside",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write(t, filepath.Join(root, "a.go"), original)

			scope := tools.NewScope(false)
			c.edit(t, root, scope)

			res, err := tools.NewUndo(root, scope).
				Run(context.Background(), mustJSON(t, map[string]string{"path": c.path}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			assertUndone(t, root, c.path, c.want, c.wantErr, c.gone, res)
		})
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// assertUndone checks one case's outcome: a refusal naming wantErr, a file
// removed, or a file restored to want.
func assertUndone(t *testing.T, root, path, want, wantErr string, gone bool, res tool.Result) {
	t.Helper()

	if wantErr != "" {
		if !res.IsError || !strings.Contains(res.Content, wantErr) {
			t.Fatalf("want an error naming %q, got %q", wantErr, res.Content)
		}

		return
	}

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	abs := filepath.Join(root, path)
	if gone {
		if _, err := os.Stat(abs); !os.IsNotExist(err) {
			t.Fatalf("want %s removed, stat gave %v", path, err)
		}

		return
	}

	got, err := os.ReadFile(abs) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	if string(got) != want {
		t.Fatalf("want %q, got %q", want, string(got))
	}
}
