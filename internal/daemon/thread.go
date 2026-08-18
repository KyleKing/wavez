package daemon

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/router"
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
	// baseline is the operation id captured before this thread's first
	// turn, so a diff covers everything the thread did rather than only
	// its most recent turn.
	baseline string
	// cycle names the phased way of working this thread runs, empty for an
	// ordinary thread, and phase is where it has reached.
	cycle string
	phase string
	// override pins every turn to one routing tier, empty for automatic
	// routing.
	override router.Choice
	thinking *bool
	state    event.State
	// samples is the recent state history one schedule lane is drawn from,
	// oldest first and bounded, since a lane covers minutes rather than a
	// thread's whole life.
	samples  []stateSample
	dirs     []string
	usage    usage
	spendUSD float64
	// compactions and tokensSaved follow the thread's own compaction events,
	// which is the only place the saving is recorded.
	compactions int
	tokensSaved int
	// processed is the Seq of the last log event folded into this cache, so
	// sync knows where to resume and never applies the same event twice.
	processed uint64
	mu        sync.Mutex
	running   bool
}

func (mt *managedThread) info() api.ThreadInfo {
	mt.sync()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	return api.ThreadInfo{
		ID:         mt.id,
		Name:       mt.name,
		Dir:        firstDir(mt.dirs),
		Dirs:       append([]string(nil), mt.dirs...),
		Parent:     mt.parent,
		Model:      mt.model,
		Cycle:      mt.cycle,
		Phase:      mt.phase,
		Thinking:   mt.thinking,
		Override:   mt.override,
		Step:       mt.step,
		State:      mt.state,
		Checkpoint: mt.baseline,
		Seq:        mt.th.Log().Head(),
		LastEvent:  mt.lastAt,
		Spend:      mt.spendUSD,
		Tokens:     mt.usage.tokens(),
		Context:    mt.usage.context,
		// The served window is the budget the router admits a turn against,
		// so a thread over it is one the router has already escalated.
		Window: router.LocalContextBudget,
	}
}

// sync folds every log event this cache has not yet seen into state, step,
// lastAt, and the other derived fields, reading straight from the thread's
// own event log rather than a second goroutine's copy of it. Append persists
// an event before it reaches any subscriber, so a sync call always catches
// up to at least what a client subscribed to this thread has already been
// sent, which is the ordering a reader like info relies on.
func (mt *managedThread) sync() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	events, err := mt.th.Log().Since(mt.processed)
	if err != nil {
		return
	}

	for i := range events {
		mt.apply(events[i])
	}
}

// apply folds one log event into the cache. Callers must hold mt.mu.
func (mt *managedThread) apply(ev event.Event) {
	if ev.Seq <= mt.processed {
		return
	}
	mt.processed = ev.Seq

	mt.lastAt = ev.At
	if ev.Kind == event.KindState {
		mt.state = ev.State
		mt.samples = append(mt.samples, stateSample{at: ev.At, state: ev.State})

		if len(mt.samples) > laneSamples {
			mt.samples = mt.samples[len(mt.samples)-laneSamples:]
		}
	}
	if v, ok := usageFromEvent(ev); ok {
		mt.usage.add(v)
	}
	if saved, ok := compactionFromEvent(ev); ok {
		mt.compactions++
		mt.tokensSaved += saved
	}
	if step := stepText(ev); step != "" {
		mt.step = step
	}
	if phase, ok := phaseOf(ev); ok {
		mt.phase = phase
	}
}

// compactionFromEvent reads the saving a compaction pass recorded on its own
// event, which is what makes the panel's compaction row a view rather than
// new instrumentation.
func compactionFromEvent(ev event.Event) (int, bool) {
	if ev.Kind != event.KindUsage {
		return 0, false
	}

	switch v := ev.Detail["tokens_saved"].(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// stepText renders what a thread is doing as words, since Home shows this
// column rather than the raw event. Streamed agent text is deliberately not
// echoed here: one token is one event, so the column would flicker per token.
func stepText(ev event.Event) string {
	switch ev.Kind {
	case event.KindState:
		// A transition may carry its own words (which lock, which rival held
		// the memory), and those say more than the state's name does.
		if ev.Text != "" {
			return ev.Text
		}

		return stateText(ev.State)
	case event.KindTool:
		if ev.Tool != "" {
			return ev.Tool
		}

		return "running a tool"
	case event.KindGate:
		return "gate " + ev.Tool
	case event.KindCycle:
		return firstLine(ev.Text)
	case event.KindHypothesis:
		return "recording what it found"
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

// phaseOf reads the phase a cycle event belongs to, so Home shows where a
// cycle has reached rather than only that one is running.
func phaseOf(ev event.Event) (string, bool) {
	if ev.Kind != event.KindCycle {
		return "", false
	}

	phase, ok := ev.Detail["phase"].(string)

	return phase, ok
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
