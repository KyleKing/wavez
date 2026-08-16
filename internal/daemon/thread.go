package daemon

import (
	"context"
	"strings"
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
		if step := stepText(u.Event); step != "" {
			mt.step = step
		}
		mt.mu.Unlock()
	}
}

// stepText renders what a thread is doing as words, since Home shows this
// column rather than the raw event. Streamed agent text is deliberately not
// echoed here: one token is one event, so the column would flicker per token.
func stepText(ev event.Event) string {
	switch ev.Kind {
	case event.KindState:
		return stateText(ev.State)
	case event.KindTool:
		if ev.Tool != "" {
			return ev.Tool
		}

		return "running a tool"
	case event.KindGate:
		return "gate " + ev.Tool
	case event.KindPermission:
		return "waiting for approval"
	case event.KindError:
		return firstLine(ev.Text)
	case event.KindAgent:
		return "responding"
	case event.KindUser, event.KindLedger, event.KindUsage:
		return ""
	default:
		return ""
	}
}

func stateText(state event.State) string {
	switch state {
	case event.StateWorking:
		return "working"
	case event.StateGating:
		return "running gates"
	case event.StateNeedsIn:
		return "needs input"
	case event.StateBlocked:
		return "waiting on a lock"
	case event.StateFailed:
		return "failed"
	case event.StateDone:
		return "done"
	case event.StateIdle:
		return "idle"
	default:
		return string(state)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}

	return s
}

func firstDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}

	return dirs[0]
}
