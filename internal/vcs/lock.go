package vcs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// lockName is the file every jj invocation against one repo serializes on.
// It lives in the shared store rather than beside a workspace, because
// workspaces of one repo write the same operation log.
const lockName = "wavez-jj.lock"

// lockMode is owner read/write: the lock carries no content and is only
// ever opened by this user's own processes.
const lockMode = 0o600

// inProcess serializes callers inside one process. An flock is held per
// open file description, so two goroutines opening the same path would each
// get the lock and neither would wait.
var inProcess sync.Mutex

// withRepoLock runs fn holding an exclusive lock on dir's repo store.
//
// Nearly every jj command snapshots the working copy, so a read is a write
// to the operation log and racing invocations are not safe. Four
// replay lanes sharing one repo made jj reconcile divergent operations five
// times in three minutes, and one lane's working copy came out divergent
// having lost three of the five files it had edited, with its checks then
// graded against that tree. A lock costs milliseconds against turns that
// take seconds, so lanes still overlap where the time actually goes.
//
// A store that cannot be located or locked runs fn anyway: serializing is an
// improvement on jj's own concurrency handling, not a precondition for it,
// and failing a run over a lock file would be worse than the race.
func withRepoLock(dir string, fn func() (string, error)) (string, error) {
	path, err := repoLockPath(dir)
	if err != nil {
		return fn()
	}

	inProcess.Lock()
	defer inProcess.Unlock()

	//nolint:gosec // path is derived from the repo store, never caller input
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockMode)
	if err != nil {
		return fn()
	}

	defer func() { _ = f.Close() }() //nolint:errcheck // closing releases the lock either way

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fn()
	}

	//nolint:errcheck // closing the file below releases the lock either way
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	return fn()
}

// repoLockPath is where dir's shared store keeps its lock. A workspace's
// .jj/repo is a file holding the path to that store; a repo's own is the
// store directory itself.
func repoLockPath(dir string) (string, error) {
	repo := filepath.Join(dir, ".jj", "repo")

	info, err := os.Stat(repo)
	if err != nil {
		return "", fmt.Errorf("locating the jj store for %s: %w", dir, err)
	}

	if info.IsDir() {
		return filepath.Join(repo, lockName), nil
	}

	target, err := os.ReadFile(repo) //nolint:gosec // repo is .jj/repo under a directory the caller already named
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", repo, err)
	}

	resolved := strings.TrimSpace(string(target))
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(dir, ".jj", resolved)
	}

	return filepath.Join(resolved, lockName), nil
}
