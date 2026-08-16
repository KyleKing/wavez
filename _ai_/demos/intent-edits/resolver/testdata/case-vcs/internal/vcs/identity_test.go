package vcs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/gh-repo-dashboard/internal/vcs"
)

func TestCheckoutIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, "app")
	worktree := filepath.Join(root, "app-wt")
	workspace := filepath.Join(root, "app-ws")
	plain := filepath.Join(root, "other")

	mkdir(t, filepath.Join(parent, ".git"))
	mkdir(t, filepath.Join(parent, ".jj", "repo"))
	mkdir(t, filepath.Join(plain, ".git"))
	mkdir(t, worktree)
	mkdir(t, filepath.Join(workspace, ".jj"))

	writeFile(t, filepath.Join(worktree, ".git"),
		"gitdir: "+filepath.Join(parent, ".git", "worktrees", "app-wt")+"\n")
	writeFile(t, filepath.Join(workspace, ".jj", "repo"),
		filepath.Join(parent, ".jj", "repo"))

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"plain repo is its own identity", plain, plain},
		{"git worktree resolves to its parent", worktree, parent},
		{"jj workspace resolves to its parent", workspace, parent},
		{"missing path is its own identity", filepath.Join(root, "gone"), filepath.Join(root, "gone")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := vcs.CheckoutIdentity(tt.path); got != tt.expected {
				t.Errorf("CheckoutIdentity(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
