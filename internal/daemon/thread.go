package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/thread"
)

// managedThread pairs a thread.Thread with the daemon-owned bookkeeping the
// manager needs: run state, cancellation, and a cached view of the thread's
// state and last activity derived from its own event log rather than from
// thread.Thread's unsynchronized fields.
type managedThread struct {
	lastAt  time.Time
	created time.Time
	lastErr error
	th      *thread.Thread
	cancel  context.CancelFunc
	done    chan struct{}
	name    string
	model   string
	parent  string
	step    string
	id      string
	state   event.State
	dirs    []string
	mu      sync.Mutex
	running bool
}

func (mt *managedThread) currentState() event.State {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	return mt.state
}

func (mt *managedThread) info() api.ThreadInfo {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	return api.ThreadInfo{
		ID:        mt.id,
		Name:      mt.name,
		Dir:       firstDir(mt.dirs),
		Dirs:      append([]string(nil), mt.dirs...),
		Parent:    mt.parent,
		Model:     mt.model,
		Step:      mt.step,
		State:     mt.state,
		Seq:       mt.th.Log().Head(),
		LastEvent: mt.lastAt,
	}
}

// watch keeps state, step, and lastAt current by following the thread's own
// event log, reusing eventlog's subscription rather than reading
// thread.Thread's fields from a second goroutine while a turn may be
// writing them.
func (mt *managedThread) watch(ctx context.Context) {
	updates, err := mt.th.Log().Subscribe(ctx, 0)
	if err != nil {
		return
	}

	for u := range updates {
		if u.Lagged {
			continue
		}

		mt.mu.Lock()
		mt.lastAt = u.Event.At
		if u.Event.Kind == event.KindState {
			mt.state = u.Event.State
		}
		mt.step = stepText(u.Event)
		mt.mu.Unlock()
	}
}

func stepText(ev event.Event) string {
	if ev.Text != "" {
		return ev.Text
	}
	if ev.Tool != "" {
		return ev.Tool
	}

	return string(ev.Kind)
}

func firstDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}

	return dirs[0]
}
