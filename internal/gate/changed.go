package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	gitTimeout = 30 * time.Second
	// The porcelainPrefix is the two status letters and the space in front of
	// each path in `git status --porcelain`.
	porcelainPrefix = 3
)

// ErrNotRepository is returned by Changed when root is in neither a git
// working tree nor a jj workspace, or the tool that would say so is not
// installed.
var ErrNotRepository = errors.New("not a git repository")

// Changed returns the files git reports as modified, added, deleted,
// renamed, or untracked under root, relative to root. Both names of a rename
// are returned: the old one no longer exists and still selects the checks
// for where it lived. A jj workspace that git cannot read, because its git
// store is inside .jj rather than at the root, is asked through jj instead:
// the files the working-copy commit changes. A workspace colocated with git
// is read through git, which sees the same working-copy changes.
func Changed(ctx context.Context, root string) ([]string, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}

	changed, err := gitChanged(ctx, root)
	if !errors.Is(err, ErrNotRepository) {
		return changed, err
	}

	if changed, ok, jjErr := jjChanged(ctx, root); ok {
		return changed, jjErr
	}

	return nil, err
}

func gitChanged(ctx context.Context, root string) ([]string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("%w: git not on PATH", ErrNotRepository)
	}

	top := run(ctx, invocation{
		dir: root, name: git, timeout: gitTimeout, args: []string{"rev-parse", "--show-toplevel"},
	})
	if top.reason != "" || top.exit != 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, root)
	}

	status := run(ctx, invocation{
		dir: root, name: git, timeout: gitTimeout,
		args: []string{"status", "--porcelain", "-z", "--untracked-files=all"},
	})
	if status.reason != "" || status.exit != 0 {
		return nil, &StatusError{Detail: oneLine(status.reason + "\n" + status.output)}
	}

	toplevel, err := filepath.EvalSymlinks(strings.TrimSpace(top.output))
	if err != nil {
		return nil, fmt.Errorf("resolving git toplevel: %w", err)
	}

	var out []string

	for _, p := range porcelainPaths(status.output) {
		rel, err := filepath.Rel(root, filepath.Join(toplevel, filepath.FromSlash(p)))
		if err != nil {
			continue
		}

		out = append(out, filepath.ToSlash(rel))
	}

	return normalize(out), nil
}

// jjChanged asks jj for the files the working-copy commit changes, and
// reports false when root is not in a jj workspace or jj is not installed.
// Since jj snapshots the working copy before it answers, a file a shell
// command wrote is included.
func jjChanged(ctx context.Context, root string) ([]string, bool, error) {
	if _, inWorkspace := nearestDir(root, ".jj"); !inWorkspace {
		return nil, false, nil
	}

	jj, found := lookPath("jj")
	if !found {
		return nil, false, nil
	}

	top := run(ctx, invocation{dir: root, name: jj, timeout: gitTimeout, args: []string{"root"}})
	if top.reason != "" || top.exit != 0 {
		return nil, false, nil
	}

	workspace, err := filepath.EvalSymlinks(strings.TrimSpace(top.output))
	if err != nil {
		return nil, true, fmt.Errorf("resolving jj root: %w", err)
	}

	// Run from the workspace root, where the paths jj prints relative to
	// the working directory are relative to the workspace.
	diff := run(ctx, invocation{
		dir: workspace, name: jj, timeout: gitTimeout,
		args: []string{"diff", "--name-only", "--color=never"},
	})
	if diff.reason != "" || diff.exit != 0 {
		return nil, true, &StatusError{Tool: "jj diff", Detail: oneLine(diff.reason + "\n" + diff.output)}
	}

	var out []string

	for line := range strings.SplitSeq(diff.output, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}

		rel, err := filepath.Rel(root, filepath.Join(workspace, filepath.FromSlash(line)))
		if err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}

	return normalize(out), true, nil
}

// lookPath finds a program on PATH.
func lookPath(name string) (string, bool) {
	bin, err := exec.LookPath(name)

	return bin, err == nil
}

// nearestDir walks up from dir to the first directory holding name.
func nearestDir(dir, name string) (string, bool) {
	for {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

// StatusError is git status, or jj diff, failing inside a repository.
type StatusError struct {
	// Tool is the command that failed, "git status" when empty.
	Tool   string
	Detail string
}

func (e *StatusError) Error() string {
	if e.Tool == "" {
		return "git status: " + e.Detail
	}

	return e.Tool + ": " + e.Detail
}

// porcelainPaths reads NUL-separated `git status --porcelain -z` output. A
// rename or copy entry is followed by a second field holding the old path,
// which has no status prefix of its own. A path ending in a slash is a
// nested repository git did not look inside.
func porcelainPaths(output string) []string {
	var out []string

	fields := strings.Split(output, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) <= porcelainPrefix {
			continue
		}

		paths := []string{entry[porcelainPrefix:]}

		if (entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C') && i+1 < len(fields) {
			i++
			paths = append(paths, fields[i])
		}

		for _, p := range paths {
			if p != "" && !strings.HasSuffix(p, "/") {
				out = append(out, p)
			}
		}
	}

	return out
}
