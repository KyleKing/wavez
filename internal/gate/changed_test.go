package gate_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// git runs cmd in dir, with an identity so a commit works on a machine that
// has none configured.
func git(t *testing.T, dir string, cmd *exec.Cmd) {
	t.Helper()

	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", cmd.Args, err, out)
	}
}

func repository(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := project(t, map[string]string{
		"kept.go": "package a\n", "edited.go": "package a\n", "removed.go": "package a\n",
		"old name.go": "package a\n\n// A long enough body for git to see the rename.\nfunc A() {}\n",
		"sub/in.go":   "package sub\n",
	})
	ctx := t.Context()
	git(t, root, exec.CommandContext(ctx, "git", "init", "-q"))
	git(t, root, exec.CommandContext(ctx, "git", "add", "-A"))
	git(t, root, exec.CommandContext(ctx, "git", "commit", "-q", "-m", "base"))

	writeFile(t, root, "edited.go", "package a\n\nfunc B() {}\n")
	writeFile(t, root, "sub/in.go", "package sub\n\nfunc C() {}\n")
	writeFile(t, root, "sub/new/untracked.go", "package n\n")
	git(t, root, exec.CommandContext(ctx, "git", "rm", "-q", "removed.go"))
	git(t, root, exec.CommandContext(ctx, "git", "mv", "old name.go", "new name.go"))

	return root
}

func TestChanged(t *testing.T) {
	t.Parallel()

	root := repository(t)

	tests := []struct {
		name string
		root string
		want []string
	}{
		{
			name: "every kind of change is listed relative to the root", root: root,
			want: []string{
				"edited.go", "new name.go", "old name.go", "removed.go", "sub/in.go", "sub/new/untracked.go",
			},
		},
		{
			name: "a root below the toplevel sees only what is under it", root: filepath.Join(root, "sub"),
			want: []string{"in.go", "new/untracked.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := gate.Changed(t.Context(), tt.root)
			if err != nil {
				t.Fatalf("Changed: %v", err)
			}

			slices.Sort(got)

			if !slices.Equal(got, tt.want) {
				t.Errorf("Changed() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChanged_OutsideARepository(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	probe := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel")
	probe.Dir = dir

	if probe.Run() == nil {
		t.Skip("the temp dir is inside a repository on this machine")
	}

	if _, err := gate.Changed(t.Context(), dir); !errors.Is(err, gate.ErrNotRepository) {
		t.Errorf("got %v, want ErrNotRepository", err)
	}

	if _, err := gate.Changed(t.Context(), filepath.Join(dir, "missing")); err == nil {
		t.Error("a root that does not exist returned no error")
	}
}
