package gate

import (
	"context"
	"errors"
	"fmt"
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

// ErrNotRepository is returned by Changed when root is not inside a git
// working tree, or git is not installed to say so.
var ErrNotRepository = errors.New("not a git repository")

// Changed returns the files git reports as modified, added, deleted,
// renamed, or untracked under root, relative to root. Both names of a rename
// are returned: the old one no longer exists and still selects the checks
// for where it lived.
func Changed(ctx context.Context, root string) ([]string, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}

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

// StatusError is git status failing inside a repository.
type StatusError struct {
	Detail string
}

func (e *StatusError) Error() string { return "git status: " + e.Detail }

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
