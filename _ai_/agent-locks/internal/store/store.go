package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kyleking/agent-locks/internal/event"
	"github.com/kyleking/agent-locks/internal/lease"
)

const (
	eventsFile      = "events.jsonl"
	stateFile       = "state.json"
	compactAfter    = 256 << 10
	leaseRetention  = 7 * 24 * time.Hour
	warnRetention   = 24 * time.Hour
	defaultStateDir = ".claude/agent-locks"
)

type Session struct {
	Root    string    `json:"root"`
	Label   string    `json:"label,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

type State struct {
	Offset   int64                   `json:"offset"`
	Leases   map[string]*lease.Lease `json:"leases"`
	Commits  map[string]time.Time    `json:"commits"`
	Warns    map[string]time.Time    `json:"warns"`
	Sessions map[string]Session      `json:"sessions"`
}

func newState() *State {
	return &State{
		Leases:   map[string]*lease.Lease{},
		Commits:  map[string]time.Time{},
		Warns:    map[string]time.Time{},
		Sessions: map[string]Session{},
	}
}

// Dir is the state directory. AGENT_LOCKS_DIR overrides it, which is what the tests
// and any throwaway experiment should set.
func Dir() string {
	if d := os.Getenv("AGENT_LOCKS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return defaultStateDir
	}
	return filepath.Join(home, defaultStateDir)
}

func path(name string) string { return filepath.Join(Dir(), name) }

// Append writes one event. Records stay under the pipe-buffer size, so O_APPEND makes
// concurrent writes from independent sessions atomic without a lock.
func Append(e event.Event) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path(eventsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Load reads the compacted snapshot and folds every event appended since, so callers
// always see current state without holding a lock.
func Load() (*State, error) {
	st := readState()
	tail, size, err := readTail(st.Offset)
	if err != nil {
		return st, err
	}
	for _, e := range tail {
		Apply(st, e)
	}
	st.Offset = size
	return st, nil
}

func readState() *State {
	b, err := os.ReadFile(path(stateFile))
	if err != nil {
		return newState()
	}
	st := newState()
	if err := json.Unmarshal(b, st); err != nil {
		return newState()
	}
	if st.Leases == nil {
		st.Leases = map[string]*lease.Lease{}
	}
	if st.Commits == nil {
		st.Commits = map[string]time.Time{}
	}
	if st.Warns == nil {
		st.Warns = map[string]time.Time{}
	}
	if st.Sessions == nil {
		st.Sessions = map[string]Session{}
	}
	return st
}

func readTail(offset int64) ([]event.Event, int64, error) {
	f, err := os.Open(path(eventsFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	// A snapshot ahead of the log means the log was rotated or truncated behind us.
	if offset > info.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	var out []event.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	consumed := offset
	for sc.Scan() {
		line := sc.Bytes()
		var e event.Event
		if err := json.Unmarshal(line, &e); err == nil {
			out = append(out, e)
		}
		consumed += int64(len(line)) + 1
	}
	return out, consumed, sc.Err()
}

func Apply(st *State, e event.Event) {
	switch e.Kind {
	case event.KindWrite, event.KindClaim:
		k := lease.Key(e.Root, e.Dir, e.Actor())
		l := st.Leases[k]
		if l == nil {
			l = &lease.Lease{
				Root:    e.Root,
				Dir:     e.Dir,
				Actor:   e.Actor(),
				Owner:   e.Owner,
				Session: e.Session,
				Agent:   e.Agent,
				First:   e.TS,
			}
			st.Leases[k] = l
		}
		l.Last = e.TS
		l.Owner = e.Owner
		if e.Kind == event.KindClaim {
			l.Manual = true
			if e.Note != "" {
				l.Label = e.Note
			}
		} else {
			l.Writes++
		}
	case event.KindRelease:
		delete(st.Leases, lease.Key(e.Root, e.Dir, e.Actor()))
	case event.KindCommit:
		if prev, ok := st.Commits[e.Root]; !ok || e.TS.After(prev) {
			st.Commits[e.Root] = e.TS
		}
	case event.KindWarn:
		st.Warns[warnKey(e.Actor(), e.Root, e.Dir, e.Peer)] = e.TS
	case event.KindSessionStart:
		st.Sessions[e.Actor()] = Session{Root: e.Root, Label: e.Note, Started: e.TS}
	case event.KindSessionEnd:
		s := st.Sessions[e.Actor()]
		s.Ended = e.TS
		st.Sessions[e.Actor()] = s
		for k, l := range st.Leases {
			if l.Actor == e.Actor() && !l.Manual {
				delete(st.Leases, k)
			}
		}
	}
}

func warnKey(actor, root, dir, peer string) string {
	return actor + "\x00" + root + "\x00" + dir + "\x00" + peer
}

func LastWarn(st *State, actor, root, dir, peer string) time.Time {
	return st.Warns[warnKey(actor, root, dir, peer)]
}

// MaybeCompact rewrites the snapshot once the unfolded tail grows past a threshold.
// It is best-effort: a session that cannot take the lock leaves compaction to the next.
func MaybeCompact(st *State) error {
	info, err := os.Stat(path(eventsFile))
	if err != nil {
		return nil
	}
	if info.Size()-snapshotOffset() < compactAfter {
		return nil
	}
	lock, err := os.OpenFile(path(stateFile+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return Save(prune(st, time.Now()))
}

func snapshotOffset() int64 {
	b, err := os.ReadFile(path(stateFile))
	if err != nil {
		return 0
	}
	var st State
	if json.Unmarshal(b, &st) != nil {
		return 0
	}
	return st.Offset
}

func prune(st *State, now time.Time) *State {
	for k, l := range st.Leases {
		if !l.Manual && now.Sub(l.Last) > leaseRetention {
			delete(st.Leases, k)
		}
	}
	for k, ts := range st.Warns {
		if now.Sub(ts) > warnRetention {
			delete(st.Warns, k)
		}
	}
	return st
}

func Save(st *State) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path(stateFile) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path(stateFile))
}

func EventsPath() string { return path(eventsFile) }
