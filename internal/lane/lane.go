// Package lane keeps second copies of a repository's tree, each at a
// revision of its own, outside the checkout. A change that is out for
// review still gets fixes while the checkout has moved on to the next one:
// the fix is made and checked in a lane at the review's head, and the
// working directory, with whatever is half done in it, is not touched and
// is not what the fix is checked against.
//
// A lane is a git worktree with a detached HEAD, kept under the user's cache
// directory so that no tool walking the checkout finds a second copy of the
// project inside it.
package lane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	dirPerm   = 0o750
	keyLength = 12
)

// ErrBusy is a lane that holds uncommitted changes made at another
// revision, which moving it would lose.
var ErrBusy = errors.New("the lane has uncommitted changes from another revision")

// ErrDiverged is a lane holding commits that the revision it was asked for
// does not have, which moving it would leave unreachable.
var ErrDiverged = errors.New("the lane has commits the revision does not: push or drop them first")

// unsafe matches what a revision's name may not carry into a directory name.
var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Lane is one copy of the tree.
type Lane struct {
	// Top is the top of the lane's tree, and Root the directory in it that
	// corresponds to the project root the lane was opened for.
	Top  string
	Root string
	// Commit is the commit the lane is at.
	Commit string
}

// Open returns the lane for rev of the repository the project at root is
// in, under cache, making it or moving it to rev as needed. A lane that
// holds uncommitted changes at rev, or commits made on top of it, is
// returned as it is, so that work in it can go on. Commit is then where the
// revision is, which the lane may be ahead of.
func Open(ctx context.Context, root, cache, rev string) (Lane, error) {
	top, err := git(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return Lane{}, fmt.Errorf("finding the repository: %w", err)
	}

	commit, err := git(ctx, root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return Lane{}, fmt.Errorf("resolving %s: %w", rev, err)
	}

	// The real path, because git prints the top with symlinks resolved.
	abs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Lane{}, fmt.Errorf("resolving %s: %w", root, err)
	}

	below, err := filepath.Rel(top, abs)
	if err != nil {
		return Lane{}, fmt.Errorf("placing %s in the repository: %w", root, err)
	}

	// The name keeps the revision readable and the hash keeps two revisions
	// whose names differ only in what a directory cannot hold apart.
	dir := filepath.Join(cache, key(top), unsafe.ReplaceAllString(rev, "-")+"-"+key(rev))
	lane := Lane{Top: dir, Root: filepath.Join(dir, below), Commit: commit}

	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
		if err := os.MkdirAll(filepath.Dir(dir), dirPerm); err != nil {
			return Lane{}, fmt.Errorf("creating the lane directory: %w", err)
		}

		// A lane whose directory was removed by hand is still registered.
		_, _ = git(ctx, top, "worktree", "prune") //nolint:errcheck // the add below reports what matters

		if _, err := git(ctx, top, "worktree", "add", "--detach", dir, commit); err != nil {
			return Lane{}, fmt.Errorf("making the lane: %w", err)
		}

		return lane, nil
	}

	return lane, moved(ctx, dir, commit)
}

// key is a short name for text that cannot be a directory name as it is.
func key(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])[:keyLength]
}

// moved puts an existing lane at commit, unless that would lose something.
// A lane with commits of its own on top of commit stays where it is: they
// are the fix that was made there, pushed or not, and work goes on from
// them. One with commits that are not in commit's history at all is
// ErrDiverged, and one with uncommitted changes from elsewhere is ErrBusy.
func moved(ctx context.Context, dir, commit string) error {
	head, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("reading the lane: %w", err)
	}

	if head == commit || ancestor(ctx, dir, commit, head) {
		return nil
	}

	if !ancestor(ctx, dir, head, commit) {
		return fmt.Errorf("%w: %s is at %s", ErrDiverged, dir, head)
	}

	dirty, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("reading the lane: %w", err)
	}

	if dirty != "" {
		return fmt.Errorf("%w: %s", ErrBusy, dir)
	}

	if _, err := git(ctx, dir, "checkout", "--detach", commit); err != nil {
		return fmt.Errorf("moving the lane: %w", err)
	}

	return nil
}

// ancestor reports whether commit a is in the history of commit b.
func ancestor(ctx context.Context, dir, a, b string) bool {
	_, err := git(ctx, dir, "merge-base", "--is-ancestor", a, b)

	return err == nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git, with arguments this package wrote
	cmd.Dir = dir

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(string(out)), nil
}
