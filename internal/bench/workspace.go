package bench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Workspace is a throwaway checkout of a task's repository at its pinned
// commit.
type Workspace struct {
	// Dir is the checkout the harness runs in.
	Dir string
	// root is the temporary directory that holds Dir.
	root string
	// origin is the repository Dir is a worktree of: the local repository,
	// or the mirror of a URL.
	origin string
}

// workspacePrefix names the temporary directories this package makes.
const workspacePrefix = "wavez-bench-"

const gitBin = "git"

// NewWorkspace checks repo out at rev under a new directory in tempDir (the
// system default when empty). A local repository gets a detached worktree,
// which shares its object store and so costs no clone. A URL gets a
// worktree of a bare mirror kept in cacheDir, one per URL, which fetches
// only the commits it lacks, so repeated runs on one large repository fetch
// it once. With no cacheDir the mirror lives in the workspace and goes with
// it.
func NewWorkspace(ctx context.Context, repo, rev, tempDir, cacheDir string) (*Workspace, error) {
	root, err := os.MkdirTemp(tempDir, workspacePrefix)
	if err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}

	ws := &Workspace{Dir: filepath.Join(root, "repo"), root: root}

	if err := ws.checkout(ctx, repo, rev, cacheDir); err != nil {
		return nil, errors.Join(err, os.RemoveAll(root))
	}

	return ws, nil
}

func (w *Workspace) checkout(ctx context.Context, repo, rev, cacheDir string) error {
	if isURL(repo) {
		if cacheDir == "" {
			cacheDir = filepath.Join(w.root, "mirror")
		}

		mirror, err := fetchRev(ctx, cacheDir, repo, rev)
		if err != nil {
			return err
		}

		w.origin = mirror
	} else {
		top, err := output(ctx, invocation{Dir: repo, Name: gitBin, Args: []string{"rev-parse", "--show-toplevel"}})
		if err != nil {
			return fmt.Errorf("finding the repository at %s: %w", repo, err)
		}

		w.origin = strings.TrimSpace(string(top))
	}

	return git(ctx, w.origin, "worktree", "add", "--quiet", "--detach", w.Dir, rev)
}

// fullRev matches a complete commit hash, which a server can be asked for
// by name. An abbreviated one has to be found among fetched branches.
var fullRev = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// mirrorKeyLength is how many hex digits of the URL's hash name its mirror.
const mirrorKeyLength = 16

// fetchRev returns the bare mirror of url in cacheDir once it holds rev.
// A full hash is fetched alone at depth one, which is a few seconds where
// a clone of the whole history is minutes. An abbreviated one, or a server
// that will not serve a commit by hash, falls back to fetching every branch
// and tag with full history.
func fetchRev(ctx context.Context, cacheDir, url, rev string) (string, error) {
	sum := sha256.Sum256([]byte(url))
	mirror := filepath.Join(cacheDir, hex.EncodeToString(sum[:])[:mirrorKeyLength]+".git")

	if err := ensureMirror(ctx, cacheDir, mirror, url); err != nil {
		return "", err
	}

	// A workspace removed by hand or by a crash leaves a worktree record
	// behind, and the mirror outlives both.
	if err := git(ctx, mirror, "worktree", "prune"); err != nil {
		return "", err
	}

	if hasCommit(ctx, mirror, rev) {
		return mirror, nil
	}

	if fullRev.MatchString(rev) {
		err := git(ctx, mirror, "fetch", "--quiet", "--depth", "1", "origin", rev)
		if err == nil && hasCommit(ctx, mirror, rev) {
			return mirror, nil
		}
	}

	args := []string{"fetch", "--quiet", "--tags", "origin"}
	if isShallow(ctx, mirror) {
		args = append(args, "--unshallow")
	}

	if err := git(ctx, mirror, args...); err != nil {
		return "", fmt.Errorf("fetching %s for %s: %w", url, rev, err)
	}

	if !hasCommit(ctx, mirror, rev) {
		return "", fmt.Errorf("%s has no commit %s: %w", url, rev, errNoCommit)
	}

	return mirror, nil
}

var errNoCommit = errors.New("not found after fetching every branch and tag")

const mirrorDirMode = 0o750

// ensureMirror makes the bare mirror of url unless it is there already. It
// is made under a temporary name and renamed into place, so a second
// benchmark starting at the same moment finds either nothing or a mirror
// that is whole.
func ensureMirror(ctx context.Context, cacheDir, mirror, url string) error {
	if _, err := os.Stat(mirror); err == nil {
		return nil
	}

	if err := os.MkdirAll(cacheDir, mirrorDirMode); err != nil {
		return fmt.Errorf("creating the repository cache: %w", err)
	}

	tmp, err := os.MkdirTemp(cacheDir, ".new-")
	if err != nil {
		return fmt.Errorf("creating the repository cache: %w", err)
	}

	err = git(ctx, tmp, "init", "--quiet", "--bare")
	if err == nil {
		err = git(ctx, tmp, "remote", "add", "origin", url)
	}

	if err == nil {
		if renameErr := os.Rename(tmp, mirror); renameErr != nil {
			if _, statErr := os.Stat(mirror); statErr != nil {
				err = fmt.Errorf("creating the mirror of %s: %w", url, renameErr)
			}
		}
	}

	if _, statErr := os.Stat(tmp); statErr == nil {
		err = errors.Join(err, os.RemoveAll(tmp))
	}

	return err
}

func isShallow(ctx context.Context, repo string) bool {
	out, err := output(ctx, invocation{Dir: repo, Name: gitBin, Args: []string{"rev-parse", "--is-shallow-repository"}})

	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func hasCommit(ctx context.Context, repo, rev string) bool {
	return git(ctx, repo, "cat-file", "-e", rev+"^{commit}") == nil
}

// Remove deletes the checkout and, for a worktree, the record of it in the
// repository it came from.
func (w *Workspace) Remove(ctx context.Context) error {
	var err error
	if w.origin != "" {
		err = git(ctx, w.origin, "worktree", "remove", "--force", w.Dir)
	}

	if rmErr := os.RemoveAll(w.root); rmErr != nil {
		err = errors.Join(err, fmt.Errorf("removing workspace: %w", rmErr))
	}

	return err
}

func git(ctx context.Context, dir string, args ...string) error {
	out, err := run(ctx, invocation{Dir: dir, Name: gitBin, Args: args})
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, firstLine(string(out)))
	}

	return nil
}
