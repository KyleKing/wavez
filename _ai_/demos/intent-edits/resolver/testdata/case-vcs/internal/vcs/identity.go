package vcs

import (
	"os"
	"path/filepath"
	"strings"
)

// CheckoutIdentity returns the path of the repository whose refs repoPath
// works on. A git worktree and a jj workspace share their parent's object
// store and branch list, so both resolve to the parent; every other checkout
// resolves to itself. Fleet-wide joins key on this to avoid counting one
// branch once per checkout that can see it.
//
// The answer comes from the pointer files git and jj write into a linked
// checkout, so it costs no subprocess and works for a repo that no longer
// exists.
func CheckoutIdentity(repoPath string) string {
	if parent, ok := gitWorktreeParent(repoPath); ok {
		return parent
	}
	if parent, ok := jjWorkspaceParent(repoPath); ok {
		return parent
	}

	return repoPath
}

// RemoteIdentity derives a key for the repository a remote URL names, host/owner/repo lowercased
func RemoteIdentity(remoteURL string) string {
	// HOLE: implement RemoteIdentity
	panic("TODO(intent): RemoteIdentity")
}

// linkedParent returns the repo repoPath borrows its refs from, or "" when
// repoPath is a standalone checkout.
func linkedParent(repoPath string) string {
	if parent := CheckoutIdentity(repoPath); parent != repoPath {
		return parent
	}

	return ""
}

// gitWorktreeParent reads a linked worktree's ".git" file, which holds
// "gitdir: <parent>/.git/worktrees/<name>".
func gitWorktreeParent(repoPath string) (string, bool) {
	contents, ok := pointerFile(filepath.Join(repoPath, ".git"))
	if !ok {
		return "", false
	}

	gitDir, ok := strings.CutPrefix(contents, "gitdir:")
	if !ok {
		return "", false
	}

	dir, name := filepath.Split(filepath.Clean(strings.TrimSpace(gitDir)))
	if name == "" || filepath.Base(filepath.Clean(dir)) != "worktrees" {
		return "", false
	}

	return filepath.Dir(filepath.Dir(filepath.Clean(dir))), true
}

// jjWorkspaceParent reads a secondary workspace's ".jj/repo" file, which holds
// the path of the shared repo directory.
func jjWorkspaceParent(repoPath string) (string, bool) {
	contents, ok := pointerFile(filepath.Join(repoPath, ".jj", "repo"))
	if !ok {
		return "", false
	}

	repoDir := filepath.Clean(strings.TrimSpace(contents))
	if filepath.Base(repoDir) != "repo" || filepath.Base(filepath.Dir(repoDir)) != ".jj" {
		return "", false
	}

	return filepath.Dir(filepath.Dir(repoDir)), true
}

// pointerFile reads path when it is a regular file rather than a directory,
// which is how git and jj mark a linked checkout.
func pointerFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}

	contents, err := os.ReadFile(path) // #nosec G304 -- path is a repo's own .git/.jj pointer
	if err != nil {
		return "", false
	}

	return string(contents), true
}
