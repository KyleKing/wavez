package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is a throwaway checkout of a task's repository at its pinned
// commit.
type Workspace struct {
	// Dir is the checkout the harness runs in.
	Dir string
	// root is the temporary directory that holds Dir.
	root string
	// origin is the repository Dir is a worktree of, empty for a clone.
	origin string
}

// workspacePrefix names the temporary directories this package makes.
const workspacePrefix = "wavez-bench-"

// NewWorkspace checks repo out at rev under a new directory in tempDir (the
// system default when empty). A local repository gets a detached worktree,
// which shares its object store and so costs no clone; a URL is cloned.
func NewWorkspace(ctx context.Context, repo, rev, tempDir string) (*Workspace, error) {
	root, err := os.MkdirTemp(tempDir, workspacePrefix)
	if err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}

	ws := &Workspace{Dir: filepath.Join(root, "repo"), root: root}

	if err := ws.checkout(ctx, repo, rev); err != nil {
		return nil, errors.Join(err, os.RemoveAll(root))
	}

	return ws, nil
}

func (w *Workspace) checkout(ctx context.Context, repo, rev string) error {
	if isURL(repo) {
		if err := git(ctx, "", "clone", "--quiet", repo, w.Dir); err != nil {
			return err
		}

		return git(ctx, w.Dir, "checkout", "--quiet", "--detach", rev)
	}

	top, err := output(ctx, invocation{Dir: repo, Name: "git", Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return fmt.Errorf("finding the repository at %s: %w", repo, err)
	}

	w.origin = strings.TrimSpace(string(top))

	return git(ctx, w.origin, "worktree", "add", "--quiet", "--detach", w.Dir, rev)
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
	out, err := run(ctx, invocation{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, firstLine(string(out)))
	}

	return nil
}
