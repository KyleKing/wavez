package goal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	dirPerm  = 0o750
	filePerm = 0o600

	// The gitTimeout bounds each question put to git about the tree.
	gitTimeout = 10 * time.Second
)

// state is the cache file: the last verdict of each expensive condition,
// by name.
type state struct {
	Items map[string]entry `json:"items"`
}

// entry is one cached verdict and what it was a verdict about. Definition
// identifies the condition as written, which anything can compare without
// starting a process. Tree identifies the working tree, which takes git.
type entry struct {
	EvaluatedAt time.Time `json:"evaluated_at"`
	Verdict     Status    `json:"verdict"`
	Detail      string    `json:"detail,omitempty"`
	Definition  string    `json:"definition"`
	Tree        string    `json:"tree"`
}

// StatePath returns where root's cached verdicts are.
func StatePath(root string) string {
	return filepath.Join(root, Dir, StateName)
}

// readState returns the cache, or an empty one when it is missing or
// unreadable: a cache that cannot be read is a cache that holds nothing.
func readState(root string) state {
	var cached state

	data, err := fs.ReadFile(os.DirFS(root), Dir+"/"+StateName)
	if err != nil || json.Unmarshal(data, &cached) != nil {
		return state{}
	}

	return cached
}

func writeState(root string, next state) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the goal state: %w", err)
	}

	return writeAtomic(StatePath(root), append(data, '\n'))
}

// writeAtomic replaces path with data through a temporary file beside it,
// so a reader never sees half a file.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", path, err)
	}

	_, err = tmp.Write(data)
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		if removeErr := os.Remove(tmp.Name()); removeErr != nil {
			err = errors.Join(err, removeErr)
		}

		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// cachedFor returns the verdict cached for the item as it is written now,
// whatever the tree has done since.
func (s state) cachedFor(item Item) (entry, bool) {
	cached, ok := s.Items[item.Name]

	return cached, ok && cached.Definition == item.definition()
}

// lookup answers an expensive item without running it: the cached verdict
// when it was reached on this tree, and unknown otherwise.
func (s state) lookup(item Item, tree string) Result {
	result := Result{Name: item.Name, Kind: item.Kind, Status: StatusUnknown, Detail: detailUnknown}

	if cached, ok := s.cachedFor(item); ok && tree != "" && cached.Tree == tree {
		result.Status, result.Detail = cached.Verdict, cached.Detail
	}

	return result
}

// treeFingerprint identifies the working tree's content cheaply: in a git
// repository, the commit plus every path git reports as changed with its
// size and modification time, and elsewhere every file's. It is empty when
// the tree could not be read, and an empty fingerprint matches nothing.
func treeFingerprint(ctx context.Context, root string) string {
	sum := sha256.New()

	if !gitFingerprint(ctx, root, sum) {
		sum.Reset()

		if !walkFingerprint(root, sum) {
			return ""
		}
	}

	return hex.EncodeToString(sum.Sum(nil)[:8])
}

func gitFingerprint(ctx context.Context, root string, sum hash.Hash) bool {
	top := run(ctx, root, gitTimeout, "git", "rev-parse", "--show-toplevel")
	if top.reason != "" || top.exit != 0 {
		return false
	}

	// An unborn branch has no HEAD, and its empty answer is still an answer.
	head := run(ctx, root, gitTimeout, "git", "rev-parse", "--verify", "--quiet", "HEAD")

	status := run(ctx, root, gitTimeout,
		"git", "--no-optional-locks", "status", "--porcelain", "-z", "--untracked-files=all", "--", ".")
	if status.reason != "" || status.exit != 0 {
		return false
	}

	toplevel := strings.TrimSpace(top.output)

	writeTo(sum, head.output)

	// Each entry is `XY path`. Git's listing alone does not change when a
	// file that was already modified is modified again, so each one's size
	// and time go in as well. Wavez's own state is left out, or writing the
	// cache would be the change that invalidates it.
	const statusPrefix = len("XY ")

	for changed := range strings.SplitSeq(status.output, "\x00") {
		if len(changed) <= statusPrefix || strings.Contains("/"+changed[statusPrefix:], "/"+Dir+"/") {
			continue
		}

		writeTo(sum, changed)

		if info, err := os.Lstat(filepath.Join(toplevel, filepath.FromSlash(changed[statusPrefix:]))); err == nil {
			writeTo(sum, strconv.FormatInt(info.Size(), 10), info.ModTime().UTC().Format(time.RFC3339Nano))
		}
	}

	return true
}

func walkFingerprint(root string, sum hash.Hash) bool {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".jj" || entry.Name() == Dir {
				return fs.SkipDir
			}

			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err //nolint:wrapcheck // only whether the walk finished is read
		}

		writeTo(sum, path, strconv.FormatInt(info.Size(), 10), info.ModTime().UTC().Format(time.RFC3339Nano))

		return nil
	})

	return err == nil
}

// writeTo feeds parts to a hash, which never fails a write.
func writeTo(sum hash.Hash, parts ...string) {
	for _, part := range parts {
		if _, err := sum.Write([]byte(part + "\x00")); err != nil {
			return
		}
	}
}
