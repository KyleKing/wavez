package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Jj is the jj backend for the primitives an agent run needs. Every method
// shells out to the jj binary; there is no library dependency.
type Jj struct{}

// NewJj builds a Jj backend.
func NewJj() *Jj {
	return &Jj{}
}

// RepoRoot returns the top-level directory of the jj repository containing
// path, wrapped as ErrNotJJRepo with InitHint when path is not inside one.
func (*Jj) RepoRoot(ctx context.Context, path string) (string, error) {
	out, err := runJJ(ctx, path, "root")
	if err != nil {
		return "", notJJRepoErr(path, err)
	}

	return strings.TrimSpace(out), nil
}

// ChangedFiles returns the repo-relative paths that differ between marker
// and the working copy, sorted and deduplicated. Marker is an operation id
// from Capture, not a commit id. An empty marker diffs from the repo's
// root commit, so every tracked file counts as changed. Jj snapshots new
// files into the working-copy commit on every command, so there is no
// separate untracked-file case the way git has one.
func (*Jj) ChangedFiles(ctx context.Context, repoRoot, marker string) ([]string, error) {
	out, err := runJJ(ctx, repoRoot, "diff", diffFromArg(marker), "--to", "@", "--name-only")
	if err != nil {
		return nil, fmt.Errorf("listing changed files since %q: %w", marker, err)
	}

	return dedupeLines(out), nil
}

// Diff returns the unified git-format diff of files between marker and the
// working copy. An empty files list diffs the whole repository. --git
// forces jj's builtin diff formatter for this call, so the output is fixed
// regardless of any --tool/ui.diff.tool configured in jj's own config;
// unlike git, jj never reads git-config's diff.external at all, so that
// setting cannot reformat this output either way.
func (*Jj) Diff(ctx context.Context, repoRoot, marker string, files []string) (string, error) {
	args := append([]string{"diff", diffFromArg(marker), "--to", "@", "--git"}, files...)

	out, err := runJJ(ctx, repoRoot, args...)
	if err != nil {
		return "", fmt.Errorf("diffing %d file(s) against %s: %w", len(files), marker, err)
	}

	return out, nil
}

// WorkingCopyDiff returns the unified git-format diff of the working-copy
// commit against its parent: what has changed but not yet been committed.
// This is a different question from Diff's, which compares against an
// operation id, and an empty marker there means the repository's root
// commit rather than the last commit.
func (*Jj) WorkingCopyDiff(ctx context.Context, repoRoot string) (string, error) {
	out, err := runJJ(ctx, repoRoot, "diff", "--from", "@-", "--to", "@", "--git")
	if err != nil {
		return "", fmt.Errorf("diffing the working copy of %s: %w", repoRoot, err)
	}

	return out, nil
}

// Capture returns the operation id of repoRoot's current state: cheap
// enough to call before every turn, since jj snapshots the working copy as
// a side effect of running any command, so this read alone is a restore
// point with no explicit snapshot step. It returns ErrNotJJRepo, wrapped
// with InitHint, when repoRoot is not a jj repository, since a checkpoint
// that cannot be taken is not a checkpoint that succeeded.
func (*Jj) Capture(ctx context.Context, repoRoot string) (string, error) {
	out, err := runJJ(ctx, repoRoot, "op", "log", "--no-graph", "--limit", "1", "-T", "id")
	if err != nil {
		return "", notJJRepoErr(repoRoot, err)
	}

	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("vcs: empty operation id for %s: %w", repoRoot, ErrNotJJRepo)
	}

	return id, nil
}

// Restore reverts repoRoot's working copy to the operation Capture
// returned as checkpoint. Safe to call when nothing changed since
// checkpoint: jj reports "Nothing changed" and Restore returns nil.
func (*Jj) Restore(ctx context.Context, repoRoot, checkpoint string) error {
	if _, err := runJJ(ctx, repoRoot, "op", "restore", checkpoint); err != nil {
		return fmt.Errorf("restoring %s to operation %s: %w", repoRoot, checkpoint, err)
	}

	return nil
}

// AddWorkspace creates a second working copy of repoRoot at dir, holding
// the same content as repoRoot's working copy, uncommitted changes
// included. A caller mutating files needs somewhere the live tree is not,
// and this is cheaper than a copy: measured on this repo, 0.31 s for 628
// files, and the Go build cache is shared with the main tree so the first
// test run in a fresh workspace costs roughly a second.
//
// The -r @ is what makes that true. Without it jj gives the new workspace
// the *parent* of the current working-copy commit, so every uncommitted
// change is missing and a caller checking uncommitted work would silently
// examine a tree without it.
//
// Dir must not exist. Pair every call with ForgetWorkspace.
func (*Jj) AddWorkspace(ctx context.Context, repoRoot, name, dir string) error {
	if _, err := runJJ(ctx, repoRoot, "workspace", "add", "-r", "@", "--name", name, dir); err != nil {
		return fmt.Errorf("adding workspace %s at %s: %w", name, dir, err)
	}

	return nil
}

// ForgetWorkspace drops the repository's record of a workspace. It does not
// delete dir, so a caller removes the directory itself.
func (*Jj) ForgetWorkspace(ctx context.Context, repoRoot, name string) error {
	if _, err := runJJ(ctx, repoRoot, "workspace", "forget", name); err != nil {
		return fmt.Errorf("forgetting workspace %s: %w", name, err)
	}

	return nil
}

func diffFromArg(marker string) string {
	if marker == "" {
		return "--from=root()"
	}

	return "--from=at_operation(" + marker + ", @)"
}

func notJJRepoErr(path string, cause error) error {
	return fmt.Errorf("%s is not a jj repository, fix with %q: %w: %w", path, InitHint, ErrNotJJRepo, cause)
}

func runJJ(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // args are fixed subcommands plus caller-supplied refs/paths, the shell-out this package exists for
	cmd := exec.CommandContext(ctx, "jj", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("jj %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

func dedupeLines(block string) []string {
	seen := make(map[string]struct{})

	var out []string

	for _, line := range strings.Split(strings.TrimSpace(block), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if _, ok := seen[line]; ok {
			continue
		}

		seen[line] = struct{}{}

		out = append(out, line)
	}

	sort.Strings(out)

	return out
}
