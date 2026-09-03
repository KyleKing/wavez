package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// registryPerm keeps the record readable only by the user whose daemon wrote
// it, since it names every command that daemon is running.
const registryPerm = 0o600

// Registry is what one daemon has running, kept on disk so a daemon that
// dies without unwinding leaves its successor something to act on.
//
// It is scoped to a single daemon by living beside that daemon's socket. A
// scratch daemon on its own socket therefore sweeps only what it spawned,
// which is the whole reason the scratch-socket convention exists: a
// user-level record shared with the daily daemon would have a starting
// scratch daemon kill the work the daily one was in the middle of.
type Registry struct {
	path string
	mu   sync.Mutex
}

// NewRegistry records into a file beside socketPath. A daemon binds its
// socket before it spawns anything, so the directory is already there.
func NewRegistry(socketPath string) *Registry {
	return &Registry{path: filepath.Join(filepath.Dir(socketPath), "spawned.json")}
}

// entry is one spawned process. Started is what pins the record to the
// process that was spawned rather than to a number: pids are reused, and a
// sweep that killed a group by pid alone would eventually kill a stranger.
type entry struct {
	Command string `json:"command"`
	Started string `json:"started"`
	PID     int    `json:"pid"`
}

// Add records a running process. A failure to record is returned rather than
// swallowed, because a caller that cannot record cannot promise cleanup.
func (r *Registry) Add(pid int, command string) error {
	started, err := startTime(context.Background(), pid)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := r.read()
	if err != nil {
		return err
	}

	return r.write(append(entries, entry{PID: pid, Command: command, Started: started}))
}

// Remove drops a process that has been waited on.
func (r *Registry) Remove(pid int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := r.read()
	if err != nil {
		return err
	}

	kept := make([]entry, 0, len(entries))

	for _, e := range entries {
		if e.PID != pid {
			kept = append(kept, e)
		}
	}

	return r.write(kept)
}

// Leak is a process a sweep found still running and killed.
type Leak struct {
	Command string
	PID     int
}

// Sweep kills every recorded process still running and empties the record.
// It is what a daemon calls at startup, where everything recorded belongs to
// a daemon that is gone.
//
// An entry whose pid is live but whose start time differs is a reused pid
// and is left alone. Anything else is dropped from the record either way, so
// a process that cannot be killed is not swept again forever.
func (r *Registry) Sweep(ctx context.Context) ([]Leak, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := r.read()
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	live, err := startTimes(ctx)
	if err != nil {
		return nil, err
	}

	var killed []Leak

	for _, e := range entries {
		if started, ok := live[e.PID]; !ok || started != e.Started {
			continue
		}

		if kerr := Kill(e.PID); kerr != nil {
			continue
		}

		killed = append(killed, Leak{PID: e.PID, Command: e.Command})
	}

	if werr := r.write(nil); werr != nil {
		return killed, werr
	}

	return killed, nil
}

func (r *Registry) read() ([]entry, error) {
	body, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", r.path, err)
	}

	// A truncated record is a daemon that died mid-write. Reporting it would
	// fail every spawn from here on over a file this package owns and can
	// rewrite, so the record starts again empty.
	var entries []entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, nil //nolint:nilerr // the record starts again empty, see above
	}

	return entries, nil
}

// write replaces the record through a temporary file, so a daemon killed
// mid-write leaves the previous record rather than half of the new one.
func (r *Registry) write(entries []entry) error {
	body, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("encoding the spawn record: %w", err)
	}

	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, registryPerm); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, r.path); err != nil {
		return fmt.Errorf("replacing %s: %w", r.path, err)
	}

	return nil
}

// startTime is when pid started, as ps reports it. Its exact format does not
// matter, since it is only ever compared with another reading of the same
// field.
func startTime(ctx context.Context, pid int) (string, error) {
	//nolint:gosec // the only argument is a pid this package was handed
	out, err := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("reading the start time of %d: %w", pid, err)
	}

	return strings.TrimSpace(string(out)), nil
}

func startTimes(ctx context.Context) (map[int]string, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,lstart=").Output()
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	times := make(map[int]string)

	for _, line := range strings.Split(string(out), "\n") {
		pid, started, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}

		n, cerr := strconv.Atoi(pid)
		if cerr != nil {
			continue
		}

		times[n] = strings.TrimSpace(started)
	}

	return times, nil
}
