package session

import (
	"context"
	"errors"
)

// ErrNoBaseline reports a session whose starting tree cannot be compared
// with: it began outside git, before the first commit, or on uncommitted
// changes too large to have been kept.
var ErrNoBaseline = errors.New("the session's starting tree was not kept")

// Diff returns what the session did to the named root-relative paths, as a
// unified diff with no context lines against the tree the session started
// from. A file the session created and git does not track is not in it.
func (s *Store) Diff(ctx context.Context, base Baseline, paths []string) (string, error) {
	if !base.Restorable() {
		return "", ErrNoBaseline
	}

	if len(paths) == 0 {
		return "", nil
	}

	from := base.Head
	if base.Tree != "" {
		from = base.Tree
	}

	args := append([]string{"diff", "--no-color", "--no-ext-diff", "--relative", "-U0", from, "--"}, paths...)

	out, err := worktree{root: s.root}.git(ctx, args...)

	return string(out), err
}
