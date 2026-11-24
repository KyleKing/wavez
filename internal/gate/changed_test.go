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
		t.Skip("NOT TESTED: git is not on PATH, so nothing that needs a repository ran")
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

// fakeJJ puts a stand-in jj first on an otherwise empty PATH, so git is not
// found and a workspace at ws is asked through jj. Its diff prints diff, or
// fails when diff is "fail".
func fakeJJ(t *testing.T, ws, diff string) {
	t.Helper()

	bin := t.TempDir()
	answer := "printf '%s\\n' " + diff
	if diff == "fail" {
		answer = "echo 'Error: the working copy is stale' >&2; exit 1"
	}

	writeFile(t, bin, "jj", "#!/bin/sh\ncase \"$1\" in\nroot) echo '"+ws+"' ;;\n"+
		"diff) [ \"$PWD\" = '"+ws+"' ] || exit 9; "+answer+" ;;\n*) exit 2 ;;\nesac\n")
	t.Setenv("PATH", bin)
}

// The fake jj replaces PATH for the whole process, so these do not run in
// parallel.
func TestChanged_AJJWorkspaceWithoutGit(t *testing.T) {
	tests := []struct {
		name    string
		sub     string
		diff    string
		want    []string
		jj      bool
		wantErr bool
	}{
		{name: "the working-copy changes", jj: true, diff: "a.go sub/b.go", want: []string{"a.go", "sub/b.go"}},
		{name: "a root below the workspace", jj: true, sub: "sub", diff: "a.go sub/b.go", want: []string{"b.go"}},
		{name: "a failing jj is an error", jj: true, diff: "fail", wantErr: true},
		{name: "a .jj without jj installed is not a repository", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			writeFile(t, ws, ".jj/repo/store/git/HEAD", "ref: refs/heads/main\n")
			writeFile(t, ws, "sub/b.go", "package sub\n")

			if tt.jj {
				fakeJJ(t, ws, tt.diff)
			} else {
				t.Setenv("PATH", t.TempDir())
			}

			got, err := gate.Changed(t.Context(), filepath.Join(ws, tt.sub))
			if (err != nil) != tt.wantErr || !slices.Equal(got, tt.want) {
				t.Errorf("Changed() = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}

			if !tt.jj && !errors.Is(err, gate.ErrNotRepository) {
				t.Errorf("got %v, want ErrNotRepository", err)
			}
		})
	}
}
