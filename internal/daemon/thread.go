package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
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
	// runStart is when the current run's prompt arrived and turnStart is
	// when its most recent turn began. They exist because a progress line
	// can honestly say how long this turn has been going against what this
	// run's turns have cost, and cannot honestly say how long the run has
	// left: over 108 runs on this project's own logs, the best remaining-run
	// estimator landed within a factor of two 23% of the time and the
	// next-turn one 54% (`_ai_/demos/progress-estimate`).
	runStart  time.Time
	turnStart time.Time
	lastErr   error
	th        *thread.Thread
	cancel    context.CancelFunc
	done      chan struct{}
	// release gives back this thread's turn admission. It is set while a
	// turn holds the scheduler and cleared while parked (a Broker prompt is
	// blocking the turn on an answer), so the same slot is never released
	// twice and a re-admission after parking replaces it rather than adding
	// a second one.
	release  func()
	thinking *bool
	// cycle names the phased way of working this thread runs, empty for an
	// ordinary thread, and phase is where it has reached.
	cycle string
	name  string
	id    string
	// baseline is the operation id captured before this thread's first
	// turn, so a diff covers everything the thread did rather than only
	// its most recent turn.
	baseline string
	// edits is one operation id per accepted change of the current run,
	// oldest first, which is what makes undo reach a single edit rather
	// than only the whole run. It is rebuilt from the log, so a reopened
	// thread keeps its picker.
	edits  []api.EditPoint
	parent string
	phase  string
	// override pins every turn to one routing tier, empty for automatic
	// routing.
	override router.Choice
	model    string
	// servedModel and servedTier are what actually answered the last turn,
	// read off its own log event. model and override are only what was
	// asked for, and a thread that pins neither still runs on some tier.
	servedModel string
	servedTier  router.Choice
	state       event.State
	step        string
	// samples is the recent state history one schedule lane is drawn from,
	// oldest first and bounded, since a lane covers minutes rather than a
	// thread's whole life.
	// pending is the prompts that arrived while a turn was running, oldest
	// first. They start one at a time at turn boundaries rather than being
	// dropped, which is what sending to a working thread used to do.
	pending []string
	samples []stateSample
	dirs    []string
	// price is what one turn cost, nil where no pricing table was wired.
	price    func(model string, usage llm.Usage) float64
	usage    usage
	spendUSD float64
	// liveSpendUSD prices the turns of the run in flight off its own log
	// events, so a metered run reports what it has spent before it ends.
	// The run's outcome replaces it at the end rather than adding to it,
	// which is why it resets there and accrues only while running.
	liveSpendUSD float64
	// turns is how many turns of the current run have finished.
	turns int
	// compactions and tokensSaved follow the thread's own compaction events,
	// which is the only place the saving is recorded.
	compactions int
	tokensSaved int
	// served is the local tier's context window the thread's project runs
	// under, fixed at creation since the loop it belongs to is.
	served int
	// processed is the Seq of the last log event folded into this cache, so
	// sync knows where to resume and never applies the same event twice.
	processed uint64
	mu        sync.Mutex
	running   bool
	// archived is whether the thread has been put away, folded from the
	// log so it survives a restart the way nothing else on this cache does.
	archived bool
	// started records that the thread has run a turn, so the thread-start
	// routines fire once rather than on every prompt.
	started bool
}

// takeRelease clears and returns the thread's held admission release, nil
// if it holds none. The caller becomes the only one that can release it.
func (mt *managedThread) takeRelease() func() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	release := mt.release
	mt.release = nil

	return release
}

func (mt *managedThread) setRelease(fn func()) {
	mt.mu.Lock()
	mt.release = fn
	mt.mu.Unlock()
}

// releaseAdmission gives back whatever admission the thread currently
// holds, or does nothing if it holds none.
func (mt *managedThread) releaseAdmission() {
	if release := mt.takeRelease(); release != nil {
		release()
	}
}

func (mt *managedThread) info() (api.ThreadInfo, error) {
	if err := mt.sync(); err != nil {
		return api.ThreadInfo{}, fmt.Errorf("syncing thread %s: %w", mt.id, err)
	}

	mt.mu.Lock()
	defer mt.mu.Unlock()

	return api.ThreadInfo{
		ID:         mt.id,
		Name:       mt.name,
		Goal:       mt.th.Goal(),
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
		Archived:   mt.archived,
		Checkpoint: mt.baseline,
		Seq:        mt.th.Log().Head(),
		LastEvent:  mt.lastAt,
		Spend:      mt.spendUSD + mt.liveSpendUSD,
		Served:     mt.servedModel,
		Tier:       mt.servedTier,
		Turn:       mt.turns + 1,
		Turns:      mt.turns,
		TurnStart:  mt.turnStart,
		TurnMean:   mt.turnMean(),
		Tokens:     mt.usage.tokens(),
		Context:    mt.usage.context,
		// The served window is the budget the router admits a turn against,
		// so a thread over it is one the router has already escalated.
		Window: mt.window(),
	}, nil
}

// tier is where this thread's turns run: what served the last one, else what
// it is pinned to, else the router's own default.
func (mt *managedThread) tier() router.Choice {
	switch {
	case mt.servedTier != "":
		return mt.servedTier
	case mt.override != "":
		return mt.override
	default:
		return router.Default
	}
}

// window is the context budget the router admits a turn against, which is
// the local tier's served context only for a thread running on it.
func (mt *managedThread) window() int {
	local := mt.served
	if local <= 0 {
		local = router.FastContextBudget
	}

	return router.ContextBudget(mt.tier(), local)
}

// sync folds every log event this cache has not yet seen into state, step,
// lastAt, and the other derived fields, reading straight from the thread's
// own event log rather than a second goroutine's copy of it. Append persists
// an event before it reaches any subscriber, so a sync call always catches
// up to at least what a client subscribed to this thread has already been
// sent, which is the ordering a reader like info relies on.
func (mt *managedThread) sync() error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	events, err := mt.th.Log().Since(mt.processed)
	if err != nil {
		return fmt.Errorf("reading thread log: %w", err)
	}

	for i := range events {
		mt.apply(events[i])
	}

	return nil
}

// apply folds one log event into the cache. Callers must hold mt.mu.
func (mt *managedThread) apply(ev event.Event) {
	if ev.Seq <= mt.processed {
		return
	}
	mt.processed = ev.Seq

	mt.lastAt = ev.At
	mt.applyTiming(ev)

	if ev.Kind == event.KindState {
		mt.state = ev.State
		mt.samples = append(mt.samples, stateSample{at: ev.At, state: ev.State})

		if len(mt.samples) > laneSamples {
			mt.samples = mt.samples[len(mt.samples)-laneSamples:]
		}
	}
	if v, ok := archivedFromEvent(ev); ok {
		mt.archived = v
	}
	if v, ok := usageFromEvent(ev); ok {
		mt.usage.add(v)
		mt.addLiveSpend(ev, v)
	}
	if model, tier, ok := servedFromEvent(ev); ok {
		mt.servedModel, mt.servedTier = model, tier
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

	mt.applyEditPoint(ev)
}

// applyEditPoint records the operation holding the tree just after one
// accepted change. A new prompt clears the list, since undo is offered over
// the run in front of the user rather than over every edit the thread has
// ever made.
func (mt *managedThread) applyEditPoint(ev event.Event) {
	if ev.Kind == event.KindUser {
		mt.edits = nil

		return
	}

	if ev.Kind != event.KindTool || len(ev.Changes) == 0 {
		return
	}

	op, ok := ev.Detail["checkpoint"].(string)
	if !ok || op == "" {
		return
	}

	paths := make([]string, 0, len(ev.Changes))
	for _, c := range ev.Changes {
		paths = append(paths, c.Path)
	}

	mt.edits = append(mt.edits, api.EditPoint{Op: op, Tool: ev.Tool, Paths: paths})
}

// applyTiming tracks the current run's turn boundaries. A run starts at the
// prompt that caused it, so the minutes a thread waits for its human are
// not counted as work; a turn boundary is an event carrying usage, which is
// where one model call ended.
func (mt *managedThread) applyTiming(ev event.Event) {
	if ev.Kind == event.KindUser {
		mt.runStart, mt.turnStart, mt.turns = ev.At, ev.At, 0

		return
	}

	if _, ok := usageFromEvent(ev); ok {
		mt.turns++
		mt.turnStart = ev.At
	}
}

// turnMean is what a turn of this run has cost so far, zero until one has
// finished.
func (mt *managedThread) turnMean() time.Duration {
	if mt.turns == 0 || mt.runStart.IsZero() {
		return 0
	}

	return mt.turnStart.Sub(mt.runStart) / time.Duration(mt.turns)
}

// archivedFromEvent reads the position a KindArchive event moves a thread to.
func archivedFromEvent(ev event.Event) (bool, bool) {
	if ev.Kind != event.KindArchive {
		return false, false
	}

	v, ok := ev.Detail["archived"].(bool)

	return v, ok
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

// addLiveSpend prices one turn into the run in flight. It accrues only while
// running, since replaying the log at startup would otherwise charge every
// turn the thread has ever taken to a run that is not happening.
func (mt *managedThread) addLiveSpend(ev event.Event, v llm.Usage) {
	if !mt.running || mt.price == nil {
		return
	}

	model, _, ok := servedFromEvent(ev)
	if !ok {
		return
	}

	mt.liveSpendUSD += mt.price(model, v)
}
