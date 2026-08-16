package vcs_test

import (
	"os"
	"path/filepath"
	"testing"

	"t1identity/vcs"
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

func TestRemoteIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"ssh url", "git@github.com:owner/repo.git", "github.com/owner/repo"},
		{"https url", "https://github.com/owner/repo", "github.com/owner/repo"},
		{"http url", "http://github.com/owner/repo.git", "github.com/owner/repo"},
		{"case folds", "git@github.com:Acme/App.git", "github.com/acme/app"},
		{"https with credentials", "https://token@github.com/owner/repo.git", "github.com/owner/repo"},
		{"ssh scheme with port", "ssh://git@github.acme.com:2222/owner/repo.git", "github.acme.com/owner/repo"},
		{"gitlab subgroup keeps its full path", "git@gitlab.com:group/sub/repo.git", "gitlab.com/group/sub/repo"},
		{"enterprise host is distinct", "git@github.acme.com:acme/app.git", "github.acme.com/acme/app"},
		{"host without a repo path", "https://github.com/owner", ""},
		{"not a url", "invalid", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := vcs.RemoteIdentity(tt.url); got != tt.expected {
				t.Errorf("RemoteIdentity(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

func mkdir(tb testing.TB, path string) {
	tb.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		tb.Fatal(err)
	}
}

func writeFile(tb testing.TB, path, contents string) {
	tb.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		tb.Fatal(err)
	}
}
