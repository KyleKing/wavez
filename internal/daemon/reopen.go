package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/thread"
)

// reopen loads every thread log already on m.logDir into m, so a daemon
// restart still shows the threads an earlier process created. A log that
// cannot be opened or decoded is skipped and warned about rather than
// failing the whole project load; only a failure to read the directory
// itself is returned. A missing directory means there is nothing to
// reopen, not an error.
func (m *manager) reopen() error {
	entries, err := os.ReadDir(m.logDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading log dir %s: %w", m.logDir, err)
	}

	var loaded []*managedThread

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if _, ok := m.get(id); ok {
			continue
		}

		mt, rerr := m.reopenThread(id, filepath.Join(m.logDir, entry.Name()))
		if rerr != nil {
			slog.Warn("reopening thread log", "id", id, "err", rerr)

			continue
		}

		loaded = append(loaded, mt)
	}

	sort.Slice(loaded, func(i, j int) bool { return loaded[i].created.Before(loaded[j].created) })

	m.mu.Lock()
	for _, mt := range loaded {
		m.threads[mt.id] = mt
		m.order = append(m.order, mt.id)
	}
	m.mu.Unlock()

	return nil
}

// reopenThread opens one thread log and folds it into a managedThread ready
// to register. A thread whose folded state shows a turn in flight
// (working, gating, needs_input, blocked) had that turn die with the
// previous process, so it is normalized back to idle on its own log before
// being returned.
func (m *manager) reopenThread(id, path string) (*managedThread, error) {
	th, err := thread.Open(m.logDir, thread.ID(id), m.defaultDirs)
	if err != nil {
		return nil, fmt.Errorf("opening thread: %w", err)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		return nil, fmt.Errorf("reading thread log: %w", err)
	}

	created, err := reopenCreated(path, events)
	if err != nil {
		return nil, err
	}

	mt := &managedThread{
		th:      th,
		id:      id,
		dirs:    m.defaultDirs,
		name:    slugName(firstUserText(events), id),
		created: created,
		state:   event.StateIdle,
	}

	if err := mt.sync(); err != nil {
		return nil, fmt.Errorf("syncing thread: %w", err)
	}

	if !interruptedState(mt.state) {
		return mt, nil
	}

	if _, err := th.Log().Append(event.Event{Kind: event.KindState, State: event.StateIdle}); err != nil {
		return nil, fmt.Errorf("normalizing interrupted thread: %w", err)
	}
	if err := mt.sync(); err != nil {
		return nil, fmt.Errorf("syncing thread: %w", err)
	}

	return mt, nil
}

// reopenCreated is the time a reopened thread is treated as having started:
// its first event's timestamp, or the log file's mtime when the log holds
// no events yet.
func reopenCreated(path string, events []event.Event) (time.Time, error) {
	if len(events) > 0 {
		return events[0].At, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat %s: %w", path, err)
	}

	return info.ModTime(), nil
}

// firstUserText answers the prompt a thread was created with, which the log
// never stores directly: it is the Text of the first KindUser event.
func firstUserText(events []event.Event) string {
	for i := range events {
		if events[i].Kind == event.KindUser {
			return events[i].Text
		}
	}

	return ""
}

func interruptedState(state event.State) bool {
	switch state {
	case event.StateWorking, event.StateGating, event.StateNeedsIn, event.StateBlocked:
		return true
	default:
		return false
	}
}
