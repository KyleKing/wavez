package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// The treeTimeout bounds each git command that builds or removes the
	// baseline tree.
	treeTimeout = time.Minute
	// The envTimeout bounds asking go what it is.
	envTimeout = 10 * time.Second
	// The treePrefix names the temporary directory a baseline tree is
	// checked out under, and staleTree is how old one must be before
	// another run may remove it.
	treePrefix = "wavez-baseline-"
	// The gitWorktree subcommand manages the checkouts of one repository.
	gitWorktree = "worktree"
	staleTree   = time.Hour
)

// Baseline names the tree a session started from, which is what a failure
// is compared against before it is allowed to block: a failure that tree
// already had is not the session's to fix.
type Baseline struct {
	// Commit is the commit that was checked out.
	Commit string
	// Tree is a git tree object holding the working tree as it then was,
	// for a session that started with uncommitted changes. It is empty
	// when there were none, and Commit is the whole of the baseline.
	Tree string
}

// ref is the object that identifies the baseline's content.
func (b Baseline) ref() string {
	if b.Tree != "" {
		return b.Tree
	}

	return b.Commit
}

type baselineKey struct{}

// WithBaseline returns a context that makes a stop-stage Run compare its
// blocking failures against base. Run's own signature is frozen and a
// baseline belongs to one call, not to the Runner, so it travels here.
// Without one a run reports every failure as the change's, which is right
// for `wavez gate` by hand: it has no session to have started from.
func WithBaseline(ctx context.Context, base Baseline) context.Context {
	return context.WithValue(ctx, baselineKey{}, base)
}

func baselineFrom(ctx context.Context) (Baseline, bool) {
	base, ok := ctx.Value(baselineKey{}).(Baseline)

	return base, ok && base.Commit != ""
}

// comparison answers what the baseline tree says about a failure. The tree
// is checked out the first time an answer is not already cached, so a run
// whose every answer is cached never builds it.
type comparison struct {
	r    *runner
	base Baseline
	root string
	// tmp is the temporary directory holding the tree, once made.
	tmp string
	// dir is the project root inside the tree.
	dir string
	// reason says why the tree could not be made.
	reason string
	goEnv  string
	cache  cache
	asked  bool
	envSet bool
}

// tree returns the project root inside a checkout of the baseline, or the
// reason there is none.
//
// The checkout is a detached `git worktree` in a temporary directory. That
// leaves the working tree and the index alone, and unlike an export it is
// still a repository, so a test that asks git about its own project sees
// what it would have seen. For a baseline with uncommitted changes the
// worktree is filled from the kept tree object instead of the commit.
// Ignored files are in neither, so a test that needs one (a local .env, a
// generated file) fails here for that reason and reads as already failing.
func (c *comparison) tree(ctx context.Context) (string, string) {
	if !c.asked {
		c.asked = true
		c.dir, c.reason = c.checkout(ctx)
	}

	return c.dir, c.reason
}

func (c *comparison) checkout(ctx context.Context) (string, string) {
	git, err := c.r.lookPath("git")
	if err != nil {
		return "", "git not on PATH"
	}

	prefix := run(ctx, invocation{
		dir: c.root, name: git, timeout: treeTimeout, args: []string{"rev-parse", "--show-prefix"},
	})
	if prefix.reason != "" || prefix.exit != 0 {
		return "", "the project is not in a git repository"
	}

	sweepTrees(ctx, git, c.root)

	tmp, err := os.MkdirTemp("", treePrefix+"*")
	if err != nil {
		return "", oneLine(err.Error())
	}

	// Go reports paths with symlinks followed, and a temporary directory
	// is behind one on some systems.
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}

	c.tmp = tmp

	tree := filepath.Join(tmp, "tree")

	// The repository's own hooks stay out of it: `git worktree add` fires
	// post-checkout, and a hook that installs dependencies or prints has
	// no business in a throwaway tree.
	args := []string{"-c", "core.hooksPath=" + os.DevNull, gitWorktree, "add", "--detach"}
	if c.base.Tree != "" {
		args = append(args, "--no-checkout")
	}

	if reason := gitReason(run(ctx, invocation{
		dir: c.root, name: git, timeout: treeTimeout, args: append(args, tree, c.base.Commit),
	})); reason != "" {
		return "", reason
	}

	if c.base.Tree != "" {
		if reason := gitReason(run(ctx, invocation{
			dir: tree, name: git, timeout: treeTimeout, args: []string{"read-tree", "--reset", "-u", c.base.Tree},
		})); reason != "" {
			return "", reason
		}
	}

	return filepath.Join(tree, filepath.FromSlash(strings.TrimSpace(prefix.output))), ""
}

// sweepTrees clears what a killed run left behind. A run that is killed
// outright never reaches close, so its tree stays in the temporary
// directory and its registration in the repository. A tree older than
// staleTree belongs to no run that could still be going, since a run ends
// inside its budget, and `git worktree prune` then drops every registration
// whose directory is gone.
func sweepTrees(ctx context.Context, git, root string) {
	left, err := filepath.Glob(filepath.Join(os.TempDir(), treePrefix+"*"))
	if err != nil {
		return
	}

	for _, dir := range left {
		info, err := os.Stat(dir)
		if err != nil || time.Since(info.ModTime()) < staleTree {
			continue
		}

		if err := os.RemoveAll(dir); err != nil {
			continue
		}
	}

	run(ctx, invocation{dir: root, name: git, timeout: treeTimeout, args: []string{gitWorktree, "prune"}})
}

// gitReason is why a git command did not do its work, or "".
func gitReason(out outcome) string {
	switch {
	case out.reason != "":
		return out.reason
	case out.exit != 0:
		return oneLine(out.output)
	default:
		return ""
	}
}

// close removes the tree, whether or not ctx is still alive: a canceled
// run must not leave a worktree registered in the repository.
func (c *comparison) close(ctx context.Context) {
	if c.tmp == "" {
		return
	}

	ctx = context.WithoutCancel(ctx)

	if git, err := c.r.lookPath("git"); err == nil {
		run(ctx, invocation{
			dir: c.root, name: git, timeout: treeTimeout,
			args: []string{gitWorktree, "remove", "--force", filepath.Join(c.tmp, "tree")},
		})
	}

	// A registration git could not remove is one it prunes by itself once
	// the directory is gone.
	if err := os.RemoveAll(c.tmp); err != nil {
		return
	}
}

// toolchain identifies the go that would run a test: its version and
// target. A baseline's answer is cached under it, because a test can fail
// on one platform and pass on another. It is "" when go cannot say.
func (c *comparison) toolchain(ctx context.Context, goBin string) string {
	if !c.envSet {
		c.envSet = true

		out := run(ctx, invocation{
			dir: c.root, name: goBin, timeout: envTimeout, args: []string{"env", "GOVERSION", "GOOS", "GOARCH"},
		})
		if out.reason == "" && out.exit == 0 {
			c.goEnv = strings.Join(strings.Fields(out.output), " ")
		}
	}

	return c.goEnv
}
