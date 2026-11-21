package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// hashLen is how many bytes of a SHA-256 a content hash keeps. Sixteen is
// far past any chance of two versions of one file colliding and halves the
// state file.
const hashLen = 16

// statusFieldLen is the "XY " prefix of a porcelain v1 status entry.
const statusFieldLen = 3

// worktree asks git about the tree under root. Root may be a subdirectory
// of the repository; every path it returns is relative to root.
type worktree struct {
	root string
}

// git runs one git command in root and returns its stdout.
func (w worktree) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed program; callers pass flags and revisions
	cmd.Dir = w.root
	// A hook must not hold the index lock an agent's own git command wants.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return out, nil
}

// prefix returns root's path inside its repository ("" at the top level,
// "sub/" below it), and false when root is not in a git work tree.
func (w worktree) prefix(ctx context.Context) (string, bool) {
	out, err := w.git(ctx, "rev-parse", "--show-prefix")
	if err != nil {
		return "", false
	}

	return strings.TrimSpace(string(out)), true
}

// head returns the commit HEAD names, or "" in a repository with no commit.
func (w worktree) head(ctx context.Context) string {
	out, err := w.git(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

// dirty lists the files under root that differ from HEAD, untracked files
// included and ignored files excluded.
func (w worktree) dirty(ctx context.Context, prefix string) ([]string, error) {
	out, err := w.git(ctx, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all", "--", ".")
	if err != nil {
		return nil, err
	}

	var paths []string

	for entry := range strings.SplitSeq(string(out), "\x00") {
		if len(entry) <= statusFieldLen {
			continue
		}

		path := entry[statusFieldLen:]
		// A nested repository is listed as a directory, which no check
		// can be pointed at.
		if strings.HasSuffix(path, "/") {
			continue
		}

		paths = append(paths, strings.TrimPrefix(path, prefix))
	}

	return paths, nil
}

// differsFrom lists the tracked files under root whose working-tree content
// is not what rev holds.
func (w worktree) differsFrom(ctx context.Context, rev string) ([]string, error) {
	out, err := w.git(ctx, "diff", "--name-only", "-z", "--no-renames", "--relative", rev, "--", ".")
	if err != nil {
		return nil, err
	}

	return splitNul(out), nil
}

// tracked lists every file under root that git tracks.
func (w worktree) tracked(ctx context.Context) ([]string, error) {
	out, err := w.git(ctx, "ls-files", "-z")
	if err != nil {
		return nil, err
	}

	return splitNul(out), nil
}

func splitNul(out []byte) []string {
	var paths []string

	for path := range strings.SplitSeq(string(out), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}

	return paths
}

// fileHash describes the current state of a root-relative path: "" when it
// does not exist, and otherwise a value that changes when its content does.
func fileHash(root, rel string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))

	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("inspecting %s: %w", rel, err)
	}

	switch {
	case info.IsDir():
		return "dir", nil
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return "", fmt.Errorf("reading link %s: %w", rel, err)
		}

		return "link:" + target, nil
	case !info.Mode().IsRegular():
		return "special", nil
	}

	return contentHash(full, rel)
}

func contentHash(full, rel string) (string, error) {
	file, err := os.Open(full) //nolint:gosec // the path is a file of the project the hook was installed in
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", rel, err)
	}

	sum := sha256.New()
	_, err = io.Copy(sum, file)
	closeErr := file.Close()

	if err = errors.Join(err, closeErr); err != nil {
		return "", fmt.Errorf("hashing %s: %w", rel, err)
	}

	return hex.EncodeToString(sum.Sum(nil)[:hashLen]), nil
}
