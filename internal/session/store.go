package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Dir is the directory under a project root that holds wavez's own state.
// It carries a .gitignore of "*", so nothing in it is ever a change.
const Dir = ".wavez"

const (
	sessionsDir = "sessions"
	dirPerm     = 0o750
	filePerm    = 0o600

	lockWait = 5 * time.Second
	lockPoll = 15 * time.Millisecond
)

// ErrInvalidID reports a session id that cannot name a state file.
var ErrInvalidID = errors.New("invalid session id")

// ErrLocked reports that another hook process held the session's state for
// longer than a hook is willing to wait.
var ErrLocked = errors.New("session state is locked")

// Store reads and writes the session states of one project root.
type Store struct {
	root string
}

// NewStore returns the store for the project at root.
func NewStore(root string) *Store {
	return &Store{root: root}
}

// Path returns where the state of session id is kept.
func (s *Store) Path(id string) string {
	return filepath.Join(s.root, Dir, sessionsDir, id+".json")
}

// Update loads the state of session id, lets fn change it, and writes it
// back, all under a lock that keeps two hooks of one session from losing
// each other's writes. A session seen for the first time, or one whose file
// is unreadable, starts from a baseline of the working tree as it is now.
// The state is not written when fn returns an error.
func (s *Store) Update(ctx context.Context, id string, fn func(*State) error) (State, error) {
	if err := validID(id); err != nil {
		return State{}, err
	}

	if err := s.Prepare(); err != nil {
		return State{}, err
	}

	unlock, err := s.lock(ctx, id)
	if err != nil {
		return State{}, err
	}

	state, err := s.apply(ctx, id, fn)
	if err := errors.Join(err, unlock()); err != nil {
		return State{}, err
	}

	return state, nil
}

// Prepare creates the directory session state lives in. Whoever creates
// .wavez also makes it ignore itself, so that a project whose hooks came
// from a user-level settings file, and so never ran `wavez init`, does not
// find wavez's state in its git status.
func (s *Store) Prepare() error {
	if err := os.MkdirAll(filepath.Join(s.root, Dir, sessionsDir), dirPerm); err != nil {
		return fmt.Errorf("creating session directory: %w", err)
	}

	ignore := filepath.Join(s.root, Dir, ".gitignore")
	if _, err := os.Lstat(ignore); err == nil {
		return nil
	}

	if err := os.WriteFile(ignore, []byte("*\n"), filePerm); err != nil {
		return fmt.Errorf("writing %s: %w", ignore, err)
	}

	return nil
}

func (s *Store) apply(ctx context.Context, id string, fn func(*State) error) (State, error) {
	state, ok := s.load(id)
	if !ok {
		var err error

		state, err = s.fresh(ctx, id)
		if err != nil {
			return State{}, err
		}
	}

	if err := fn(&state); err != nil {
		return State{}, err
	}

	return state, s.save(state)
}

func validID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`+"\x00") {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}

	return nil
}

// Read returns what is stored about a session without taking its lock or
// writing anything, and false when nothing is stored. It is for a reader
// that only shows the state, such as the status line, and may see a write
// that is one event old.
func (s *Store) Read(id string) (State, bool) {
	if id == "" {
		return State{}, false
	}

	return s.load(id)
}

// load returns the stored state, and false when there is none worth
// keeping: no file, a truncated one, or one from another schema version.
func (s *Store) load(id string) (State, bool) {
	raw, err := os.ReadFile(s.Path(id))
	if err != nil {
		return State{}, false
	}

	var state State
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != Version {
		return State{}, false
	}

	return state, true
}

func (s *Store) fresh(ctx context.Context, id string) (State, error) {
	baseline, err := s.baseline(ctx)
	if err != nil {
		return State{}, err
	}

	return State{Version: Version, SessionID: id, Recorded: []string{}, Baseline: baseline}, nil
}

func (s *Store) baseline(ctx context.Context) (Baseline, error) {
	tree := worktree{root: s.root}

	prefix, ok := tree.prefix(ctx)
	if !ok {
		return Baseline{}, nil
	}

	paths, err := tree.dirty(ctx, prefix)
	if err != nil {
		return Baseline{}, fmt.Errorf("taking baseline: %w", err)
	}

	dirty := make(map[string]string, len(paths))

	for _, path := range paths {
		if internal(path) {
			continue
		}

		dirty[path], err = fileHash(s.root, path)
		if err != nil {
			return Baseline{}, fmt.Errorf("taking baseline: %w", err)
		}
	}

	base := Baseline{Git: true, Head: tree.head(ctx), Dirty: dirty}
	if base.Head != "" {
		base.Tree = tree.snapshot(ctx, slices.Sorted(maps.Keys(dirty)))
	}

	return base, nil
}

// save writes the state through a temporary file, so a reader never sees
// half of it.
func (s *Store) save(state State) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding session state: %w", err)
	}

	path := s.Path(state.SessionID)

	tmp, err := os.CreateTemp(filepath.Dir(path), state.SessionID+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing session state: %w", err)
	}

	_, err = tmp.Write(append(raw, '\n'))
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return errors.Join(fmt.Errorf("writing session state: %w", err), os.Remove(tmp.Name()))
	}

	return nil
}

// lock takes an exclusive flock on the session's lock file and returns the
// function releasing it. The kernel holds the lock for the open file, so a
// hook that is killed releases it by dying, and nobody ever has to decide
// that a lock is stale. The file itself is never removed: removing it would
// let a third process lock a new file while a second still holds the old.
func (s *Store) lock(ctx context.Context, id string) (func() error, error) {
	path := strings.TrimSuffix(s.Path(id), ".json") + ".lock"

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec // id is validated
	if err != nil {
		return nil, wrapLock(err)
	}

	deadline := time.Now().Add(lockWait)

	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Closing the file is what releases the lock.
			return func() error { return wrapLock(file.Close()) }, nil
		}

		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, errors.Join(wrapLock(err), file.Close())
		}

		if time.Now().After(deadline) {
			return nil, errors.Join(fmt.Errorf("%w: %s", ErrLocked, path), file.Close())
		}

		select {
		case <-ctx.Done():
			return nil, errors.Join(wrapLock(ctx.Err()), file.Close())
		case <-time.After(lockPoll):
		}
	}
}

func wrapLock(err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("locking session state: %w", err)
}

// Changed lists what the session changed under the root: the files its edit
// tools recorded, plus every file whose content is no longer what the
// baseline saw, which is how a file rewritten by a shell command, a
// generator, or a refactor is noticed. Paths are root-relative, sorted, and
// include files that have been deleted. Outside a git repository only the
// recorded files are known.
func (s *Store) Changed(ctx context.Context, state State) ([]string, error) {
	changed := make(map[string]struct{}, len(state.Recorded))
	for _, path := range state.Recorded {
		changed[path] = struct{}{}
	}

	if state.Baseline.Git {
		drifted, err := s.drifted(ctx, state.Baseline)
		if err != nil {
			return nil, err
		}

		for _, path := range drifted {
			changed[path] = struct{}{}
		}
	}

	maps.DeleteFunc(changed, func(path string, _ struct{}) bool { return internal(path) })

	return slices.Sorted(maps.Keys(changed)), nil
}

// drifted lists the files whose current content differs from the baseline.
func (s *Store) drifted(ctx context.Context, base Baseline) ([]string, error) {
	tree := worktree{root: s.root}

	prefix, ok := tree.prefix(ctx)
	if !ok {
		return nil, nil
	}

	candidates, err := tree.dirty(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("reading working tree: %w", err)
	}

	committed, err := committedSince(ctx, tree, base.Head)
	if err != nil {
		return nil, fmt.Errorf("reading working tree: %w", err)
	}

	candidates = append(candidates, committed...)

	var drifted []string

	for _, path := range candidates {
		if _, was := base.Dirty[path]; !was {
			drifted = append(drifted, path)
		}
	}

	for path, before := range base.Dirty {
		now, hashErr := fileHash(s.root, path)
		if hashErr != nil {
			return nil, fmt.Errorf("reading working tree: %w", hashErr)
		}

		if now != before {
			drifted = append(drifted, path)
		}
	}

	return drifted, nil
}

// committedSince lists the files that commits made after the baseline may
// have changed. Comparing against the baseline's commit, not today's HEAD,
// is what keeps a file the session committed in the change set.
func committedSince(ctx context.Context, tree worktree, baseHead string) ([]string, error) {
	head := tree.head(ctx)

	switch {
	case head == baseHead:
		return nil, nil
	case baseHead == "":
		// The repository had no commit; everything in it then was dirty
		// and is in the baseline, so any tracked file outside it is new.
		return tree.tracked(ctx)
	}

	committed, err := tree.differsFrom(ctx, baseHead)
	if err != nil {
		// The baseline commit is gone (a rewritten history that was
		// pruned); what differs from HEAD is the best answer left.
		return nil, nil //nolint:nilerr // degrading to the dirty set is the intended answer
	}

	return committed, nil
}

// internal reports whether a path is wavez's own state.
func internal(path string) bool {
	return path == Dir || strings.HasPrefix(path, Dir+"/")
}

// Fingerprint identifies a change set by the paths in it and their current
// content, so the same files with the same bytes always give the same value
// and any further edit gives another.
func Fingerprint(root string, files []string) (string, error) {
	sorted := slices.Clone(files)
	slices.Sort(sorted)

	sum := sha256.New()

	for _, path := range sorted {
		hash, err := fileHash(root, path)
		if err != nil {
			return "", err
		}

		sum.Write([]byte(path + "\x00" + hash + "\x00"))
	}

	return hex.EncodeToString(sum.Sum(nil)[:hashLen]), nil
}
