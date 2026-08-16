package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Git is the git backend for the primitives a gate needs. Every method
// shells out to the git binary; there is no library dependency.
type Git struct{}

// NewGit builds a Git backend.
func NewGit() *Git {
	return &Git{}
}

// RepoRoot returns the top-level directory of the git repository containing
// path.
func (*Git) RepoRoot(ctx context.Context, path string) (string, error) {
	out, err := runGit(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolving repo root for %s: %w", path, err)
	}

	return strings.TrimSpace(out), nil
}

// ChangedFiles returns the repo-relative paths that differ between marker
// and the working tree, plus untracked files, sorted and deduplicated.
// Marker is a gate's own last-known-good SHA (DESIGN.md's "Gates" section
// is explicit that this is never a session ID), not the current HEAD.
// An empty marker means no prior gate run exists yet, so every tracked and
// untracked file counts as changed.
func (*Git) ChangedFiles(ctx context.Context, repoRoot, marker string) ([]string, error) {
	var (
		tracked string
		err     error
	)
	if marker == "" {
		tracked, err = runGit(ctx, repoRoot, "ls-files")
	} else {
		tracked, err = runGit(ctx, repoRoot, "diff", "--name-only", marker)
	}
	if err != nil {
		return nil, fmt.Errorf("listing changed files since %q: %w", marker, err)
	}

	untracked, err := runGit(ctx, repoRoot, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("listing untracked files: %w", err)
	}

	return dedupeLines(tracked, untracked), nil
}

// Diff returns the unified diff of files between marker and the working
// tree. An empty files list diffs the whole repository.
func (*Git) Diff(ctx context.Context, repoRoot, marker string, files []string) (string, error) {
	// --no-ext-diff overrides any diff.external the user's global gitconfig
	// sets (e.g. difftastic), which otherwise replaces the unified diff
	// format this method's callers parse.
	args := append([]string{"diff", "--no-ext-diff", marker}, pathspec(files)...)

	out, err := runGit(ctx, repoRoot, args...)
	if err != nil {
		return "", fmt.Errorf("diffing %d file(s) against %s: %w", len(files), marker, err)
	}

	return out, nil
}

func pathspec(files []string) []string {
	if len(files) == 0 {
		return nil
	}

	return append([]string{"--"}, files...)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // args are fixed subcommands plus caller-supplied refs/paths, the shell-out this package exists for
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

func dedupeLines(blocks ...string) []string {
	seen := make(map[string]struct{})

	var out []string

	for _, block := range blocks {
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
	}

	sort.Strings(out)

	return out
}
