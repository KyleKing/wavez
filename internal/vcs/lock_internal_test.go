package vcs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Workspaces of one repo write the same operation log, so the lock has to
// resolve to the shared store rather than to the directory jj was invoked
// in. A workspace's .jj/repo is a file holding the path to that store.
func TestRepoLockPathResolvesToTheSharedStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := filepath.Join(root, "repo", ".jj", "repo")

	if err := os.MkdirAll(store, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	ws := filepath.Join(root, "ws", ".jj")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(filepath.Join(ws, "repo"), []byte(store+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fromRepo, err := repoLockPath(filepath.Join(root, "repo"))
	if err != nil {
		t.Fatalf("repoLockPath from the repo: %v", err)
	}

	fromWorkspace, err := repoLockPath(filepath.Join(root, "ws"))
	if err != nil {
		t.Fatalf("repoLockPath from the workspace: %v", err)
	}

	if fromRepo != fromWorkspace {
		t.Errorf("repo locks on %q and its workspace on %q, want one lock", fromRepo, fromWorkspace)
	}
}

// The lock exists so concurrent lanes cannot interleave, which is what left
// one replay workspace divergent and missing three of the five files it had
// edited. Overlapping critical sections are the failure, so the test counts
// them rather than timing anything.
func TestWithRepoLockAdmitsOneCallerAtATime(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".jj", "repo"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	var (
		mu      sync.Mutex
		inside  int
		overlap int
		wg      sync.WaitGroup
	)

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = withRepoLock(dir, func() (string, error) { //nolint:errcheck // the callback cannot fail
				mu.Lock()
				inside++

				if inside > 1 {
					overlap++
				}
				mu.Unlock()

				mu.Lock()
				inside--
				mu.Unlock()

				return "", nil
			})
		}()
	}

	wg.Wait()

	if overlap != 0 {
		t.Errorf("%d callers ran inside the lock at once, want none", overlap)
	}
}
