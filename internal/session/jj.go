package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// jjWorkspace asks jj about a workspace whose git store is inside .jj, where
// git cannot see it. Every path it returns is relative to root.
type jjWorkspace struct {
	root string
}

// runJJ runs one jj command in dir and returns its stdout.
func runJJ(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "jj", append([]string{"--color=never"}, args...)...) //nolint:gosec // fixed program
	cmd.Dir = dir

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("jj %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return out, nil
}

// start returns the commit id of the working-copy commit, which jj
// snapshots first, and false when root is in no jj workspace or jj is not
// installed. Later edits rewrite the working-copy commit under a new id, and
// the old one stays readable, so it names the tree the session started from.
func (w jjWorkspace) start(ctx context.Context) (string, bool) {
	if !inJJWorkspace(w.root) {
		return "", false
	}

	out, err := runJJ(ctx, w.root, "log", "-r", "@", "--no-graph", "-T", "commit_id")
	if err != nil {
		return "", false
	}

	id := strings.TrimSpace(string(out))

	return id, id != ""
}

// changedSince lists the files that differ between commit and the working
// copy now.
func (w jjWorkspace) changedSince(ctx context.Context, commit string) ([]string, error) {
	top, err := runJJ(ctx, w.root, "root")
	if err != nil {
		return nil, err
	}

	workspace, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	if err != nil {
		return nil, fmt.Errorf("resolving jj root: %w", err)
	}

	// From the workspace root, the paths jj prints relative to the working
	// directory are relative to the workspace.
	out, err := runJJ(ctx, workspace, "diff", "--from", commit, "--name-only")
	if err != nil {
		return nil, err
	}

	root, err := filepath.EvalSymlinks(w.root)
	if err != nil {
		return nil, fmt.Errorf("resolving root: %w", err)
	}

	var paths []string

	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}

		rel, err := filepath.Rel(root, filepath.Join(workspace, filepath.FromSlash(line)))
		if err == nil && filepath.IsLocal(rel) {
			paths = append(paths, filepath.ToSlash(rel))
		}
	}

	return paths, nil
}

// inJJWorkspace reports whether dir or a directory above it holds .jj.
func inJJWorkspace(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".jj")); err == nil {
			return true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}

		dir = parent
	}
}
