package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// snapshotLimit bounds how many bytes of uncommitted files a baseline will
// copy into git's object store. A tree dirtier than that is not kept.
const snapshotLimit = 8 << 20

// snapshot stores the uncommitted files as a git tree object and returns
// its name, or "" when it could not or the files are too large to copy.
//
// The hashes in Baseline.Dirty say that a file changed, not what it held, so
// they cannot rebuild the tree a session started from. A tree object can,
// and it keeps the state file to one more line whatever the size of the
// dirt. It is written through a throwaway copy of the index, so neither the
// index, the working tree, nor any ref is touched; the objects are
// unreachable and git prunes them in its own time. A session that outlives
// them loses the comparison, not its state.
func (w worktree) snapshot(ctx context.Context, paths []string) string {
	if len(paths) == 0 || !w.fits(paths) {
		return ""
	}

	index, err := w.scratchIndex(ctx)
	if err != nil {
		return ""
	}

	defer discard(index)

	env := "GIT_INDEX_FILE=" + index
	stdin := strings.Join(paths, "\x00") + "\x00"

	_, err = w.gitWith(ctx, env, stdin,
		"--literal-pathspecs", "add", "--all", "--pathspec-from-file=-", "--pathspec-file-nul")
	if err != nil {
		return ""
	}

	out, err := w.gitWith(ctx, env, "", "write-tree")
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

// fits reports whether the files at paths are together small enough to copy.
func (w worktree) fits(paths []string) bool {
	var total int64

	for _, path := range paths {
		info, err := os.Lstat(filepath.Join(w.root, filepath.FromSlash(path)))
		if err != nil {
			continue
		}

		if total += info.Size(); total > snapshotLimit {
			return false
		}
	}

	return true
}

// scratchIndex copies the repository's index to a temporary file, so that
// staging into it costs only the files that differ, and returns its path.
func (w worktree) scratchIndex(ctx context.Context) (string, error) {
	out, err := w.git(ctx, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}

	index := strings.TrimSpace(string(out))
	if !filepath.IsAbs(index) {
		index = filepath.Join(w.root, index)
	}

	scratch, err := os.CreateTemp("", "wavez-index-*")
	if err != nil {
		return "", fmt.Errorf("creating scratch index: %w", err)
	}

	err = copyInto(scratch, index)
	if err = errors.Join(err, scratch.Close()); err != nil {
		return "", errors.Join(fmt.Errorf("copying index: %w", err), os.Remove(scratch.Name()))
	}

	return scratch.Name(), nil
}

// discard removes a scratch file. One left behind in the temporary
// directory harms nothing, so a failure to remove it is not reported.
func discard(path string) {
	if err := os.Remove(path); err != nil {
		return
	}
}

// copyInto copies the file at src into dst. A repository whose index does
// not exist yet has nothing staged, which an empty copy says as well.
func copyInto(dst io.Writer, src string) error {
	file, err := os.Open(src) //nolint:gosec // the path is the one git names for its own index
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}

	_, err = io.Copy(dst, file)

	return errors.Join(err, file.Close())
}

// gitWith runs one git command in root with one more environment variable
// and the given standard input, and returns its stdout.
func (w worktree) gitWith(ctx context.Context, env, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed program; callers pass flags and revisions
	cmd.Dir = w.root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", env)
	cmd.Stdin = strings.NewReader(stdin)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return out, nil
}
