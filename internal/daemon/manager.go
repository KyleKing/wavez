package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/mention"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/sched"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

// Sentinel errors manager operations return.
var (
	ErrThreadNotFound = errors.New("daemon: thread not found")
	ErrThreadBusy     = errors.New("daemon: thread is already running a turn")
	ErrNoCheckpoint   = errors.New("daemon: thread has no checkpoint yet")
	ErrNoRepository   = errors.New("daemon: no repository to restore")
	// ErrNothingToRestore reports an undo that would discard nothing, which
	// is a refusal rather than a successful no-op.
	ErrNothingToRestore  = errors.New("daemon: nothing has changed since the checkpoint")
	ErrRestoreIncomplete = errors.New("daemon: restore left the working copy changed")
	ErrUnknownTier       = errors.New("daemon: unknown routing tier")
	// ErrNoCycles reports a cycle asked of a Server built without one, which
	// is refused rather than run as an ordinary turn.
	ErrNoCycles = errors.New("daemon: this daemon runs no cycles")
)

// manager holds every live thread for the lifetime of the Server, so a
// thread survives a client disconnecting and reconnecting.
type manager struct {
	ctx       context.Context //nolint:containedctx // scopes every thread's lifetime to the manager
	loop      *agent.Loop
	cycles    CycleSource
	mentions  Expander
	cancelAll context.CancelFunc
	spend     *spendLedger
	scheduler *sched.Scheduler
	threads   map[string]*managedThread
	logDir    string
	prefix    agent.Prefix
	// defaultDirs is the directory set a thread gets when a client names
	// none, per api.Command's documented default.
	defaultDirs []string
	order       []string
	toolCalls   atomic.Int64
	malformed   atomic.Int64
	mu          sync.Mutex
}

// Expander resolves @file and @symbol references in a prompt.
// *mention.Expander satisfies it; a manager without one sends the prompt
// through unchanged.
type Expander interface {
	Expand(ctx context.Context, prompt string) (mention.Result, error)
}

func newManager(logDir string, loop *agent.Loop, prefix agent.Prefix) *manager {
	ctx, cancelAll := context.WithCancel(context.Background())

	return &manager{
		loop:      loop,
		prefix:    prefix,
		logDir:    logDir,
		ctx:       ctx,
		cancelAll: cancelAll,
		threads:   make(map[string]*managedThread),
		spend:     newSpendLedger(time.Now),
	}
}

// createParams describes a new thread.
const (
	slugWords = 5
	slugChars = 28
)

type createParams struct {
	Cycle  string
	Model  string
	Parent string
	Prompt string
	Dirs   []string
}

// slugName is the short handle every screen shows for a thread, derived from
// the prompt because a thread id is not something a person can scan a list by.
func slugName(prompt, fallback string) string {
	fields := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var parts []string

	width := 0
	for _, f := range fields {
		if len(parts) == slugWords || width+len(f) > slugChars {
			break
		}
		parts = append(parts, f)
		width += len(f) + 1
	}
	if len(parts) == 0 {
		return fallback
	}

	return strings.Join(parts, "-")
}

// create does not take a context: a thread outlives the request that
// created it, so its lifetime is m.ctx (the manager's), never a caller's.
func (m *manager) create(p createParams) (*managedThread, error) {
	if p.Cycle != "" {
		if m.cycles == nil {
			return nil, ErrNoCycles
		}
		if _, err := m.cycles.Cycle(p.Cycle); err != nil {
			return nil, fmt.Errorf("creating thread: %w", err)
		}
	}

	id := newID()

	dirs := p.Dirs
	if len(dirs) == 0 {
		dirs = m.defaultDirs
	}

	var opts []thread.Option
	if p.Model != "" {
		opts = append(opts, thread.WithModel(p.Model))
	}
	if p.Parent != "" {
		opts = append(opts, thread.WithParent(thread.ID(p.Parent)))
	}

	th, err := thread.Open(m.logDir, thread.ID(id), dirs, opts...)
	if err != nil {
		return nil, fmt.Errorf("opening thread: %w", err)
	}

	mt := &managedThread{
		th:      th,
		cycle:   p.Cycle,
		id:      id,
		dirs:    dirs,
		model:   p.Model,
		parent:  p.Parent,
		name:    slugName(p.Prompt, id),
		created: time.Now(),
		state:   event.StateIdle,
	}

	if p.Parent != "" {
		if err := m.seedFromParent(mt, p.Parent); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	m.threads[id] = mt
	m.order = append(m.order, id)
	m.mu.Unlock()

	return mt, nil
}

// seedFromParent opens a forked thread with its parent's change set and
// nothing else. DESIGN.md's Cycles measurement is the reason: 97.6% of a
// transcript is re-derivable from the tree and the tools, so carrying the
// prose buys staleness rather than context. What cannot be re-derived is
// which files the parent had already touched, so that is what crosses.
//
// A parent that has changed nothing seeds nothing, and a fork of a thread
// that no longer exists is not an error: the fork is still a usable thread.
func (m *manager) seedFromParent(child *managedThread, parentID string) error {
	parent, ok := m.get(parentID)
	if !ok {
		return nil
	}

	changes, err := accumulatedChanges(parent)
	if err != nil {
		return err
	}

	if len(changes) == 0 {
		return nil
	}

	ev := event.Event{
		Kind:     event.KindTool,
		Tool:     "fork",
		Text:     fmt.Sprintf("forked from %s, inheriting %d changed file(s)", parent.name, len(changes)),
		Changes:  changes,
		ThreadID: child.id,
	}

	if _, err := child.th.Log().Append(ev); err != nil {
		return fmt.Errorf("seeding fork of %s: %w", parentID, err)
	}

	return nil
}

// accumulatedChanges collapses a thread's log to its change set.
func accumulatedChanges(mt *managedThread) ([]tool.Change, error) {
	events, err := mt.th.Log().Since(0)
	if err != nil {
		return nil, fmt.Errorf("reading thread %s: %w", mt.id, err)
	}

	return thread.ChangeSet(events), nil
}

// defaultDirs normalizes a configured root into a directory set, so a
// Server built without one still creates usable threads.
func defaultDirs(root string) []string {
	if root == "" {
		return nil
	}

	return []string{root}
}

func (m *manager) get(id string) (*managedThread, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mt, ok := m.threads[id]

	return mt, ok
}

func (m *manager) list() []api.ThreadInfo {
	m.mu.Lock()
	order := append([]string(nil), m.order...)
	m.mu.Unlock()

	out := make([]api.ThreadInfo, 0, len(order))
	for _, id := range order {
		mt, ok := m.get(id)
		if !ok {
			continue
		}
		out = append(out, mt.info())
	}

	return out
}

func (m *manager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.threads)
}

func (m *manager) snapshot() []*managedThread {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*managedThread, 0, len(m.threads))
	for _, mt := range m.threads {
		out = append(out, mt)
	}

	return out
}

// fleetStats is one pass over every live thread for the diagnostics panel:
// the sums, the per-thread rows a panel row drills into, and the two numbers
// that belong to one thread rather than to the fleet (the occupied context
// window and the runtime's last timings).
type fleetStats struct {
	timings   *llm.Timings
	perThread []api.ThreadDiag
	usage     usage
	// context and window describe the most recently active thread, since an
	// occupied window does not add up across threads.
	context        int
	window         int
	rows           int
	needsInput     int
	compactionRuns int
	tokensSaved    int
}

func (m *manager) fleetStats() fleetStats {
	var (
		out    fleetStats
		latest time.Time
	)

	for _, mt := range m.snapshot() {
		mt.sync()

		mt.mu.Lock()
		u, lastAt := mt.usage, mt.lastAt
		out.compactionRuns += mt.compactions
		out.tokensSaved += mt.tokensSaved
		diag := api.ThreadDiag{
			ID: mt.id, Name: mt.name, Dir: firstDir(mt.dirs),
			Spend: mt.spendUSD, Tokens: u.tokens(), Context: u.context,
			Window: router.LocalContextBudget, Rows: clampRows(mt.th.Log().Head()),
		}
		state := mt.state
		mt.mu.Unlock()

		if state == event.StateNeedsIn {
			out.needsInput++
		}

		out.usage.input += u.input
		out.usage.output += u.output
		out.usage.cacheRead += u.cacheRead
		out.rows += diag.Rows
		out.perThread = append(out.perThread, diag)

		if u.context > 0 && lastAt.After(latest) {
			latest = lastAt
			out.context, out.window = u.context, router.LocalContextBudget
		}
		if u.timings != nil && !lastAt.Before(latest) {
			out.timings = u.timings
		}
	}

	sort.Slice(out.perThread, func(i, j int) bool { return out.perThread[i].Name < out.perThread[j].Name })

	return out
}

// localModel is the model the router serves local turns with, which is the
// name the diagnostics panel shows. It is what the loop is configured with,
// not a reading from a running llama-server.
func (m *manager) localModel() string {
	if m.loop == nil {
		return ""
	}

	return m.loop.LocalModel()
}

func (m *manager) toolCallCount() int { return int(m.toolCalls.Load()) }

func (m *manager) malformedCount() int { return int(m.malformed.Load()) }

// appendState records a lifecycle transition directly on threadID's event
// log, bypassing the Thread type's own SetState: its state field has no
// internal synchronization and a turn already in flight owns it, so going
// through the log instead (eventlog.Log is safe for concurrent use) lets the
// daemon flag a pending prompt without racing the turn.
func (m *manager) appendState(threadID string, state event.State) error {
	mt, ok := m.get(threadID)
	if !ok {
		return ErrThreadNotFound
	}
	if _, err := mt.th.Log().Append(event.Event{Kind: event.KindState, State: state}); err != nil {
		return fmt.Errorf("appending state: %w", err)
	}

	return nil
}

// setOverride pins threadID to one routing tier, or clears the pin when
// override is empty. It applies from the next turn: a turn already in
// flight has routed itself.
func (m *manager) setOverride(threadID string, override router.Choice) error {
	if override != "" && !override.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownTier, override)
	}

	mt, ok := m.get(threadID)
	if !ok {
		return ErrThreadNotFound
	}

	mt.mu.Lock()
	mt.override = override
	mt.mu.Unlock()

	return nil
}

// setThinking turns a hybrid model's reasoning trace on or off for
// threadID's next turn, or restores the served model's own default when
// thinking is nil. Measured on qwen3:8b through llama-server: replying "OK"
// costs 79 completion tokens with the trace on and 2 with it off, and
// decode is the local bottleneck.
func (m *manager) setThinking(threadID string, thinking *bool) error {
	mt, ok := m.get(threadID)
	if !ok {
		return ErrThreadNotFound
	}

	mt.mu.Lock()
	mt.thinking = thinking
	mt.mu.Unlock()

	return nil
}

// send starts a turn against threadID's thread, running against m.ctx (not a
// caller's context) so the turn keeps going after any connection that
// started it disconnects.
func (m *manager) send(threadID, prompt string) error {
	mt, ok := m.get(threadID)
	if !ok {
		return ErrThreadNotFound
	}

	mt.mu.Lock()
	if mt.running {
		mt.mu.Unlock()

		return ErrThreadBusy
	}
	turnCtx, cancel := context.WithCancel(m.ctx)
	mt.running = true
	mt.cancel = cancel
	done := make(chan struct{})
	mt.done = done
	mt.mu.Unlock()

	go m.runTurn(turnCtx, mt, done, prompt)

	return nil
}

func (m *manager) runTurn(ctx context.Context, mt *managedThread, done chan struct{}, prompt string) {
	defer close(done)

	mt.mu.Lock()
	override, thinking := mt.override, mt.thinking
	mt.mu.Unlock()

	runCtx := lease.WithHolder(withThreadID(ctx, mt.id), mt.id)

	release, err := m.admit(runCtx, mt.id, override)
	if err != nil {
		mt.mu.Lock()
		mt.running, mt.cancel, mt.lastErr = false, nil, err
		mt.mu.Unlock()

		return
	}
	defer release()

	route := router.Input{Override: override, Thinking: thinking}
	expanded := m.expand(runCtx, mt, prompt)

	if mt.cycle != "" {
		m.runCycle(runCtx, mt, route, expanded)

		return
	}

	outcome, err := m.loop.Run(runCtx, mt.th, m.prefix, expanded, route)

	m.toolCalls.Add(int64(outcome.ToolCalls))
	if outcome.Stop == agent.StopMalformedTool {
		m.malformed.Add(1)
	}
	m.spend.add(outcome.HostedSpendUSD)

	if err != nil && !errors.Is(err, context.Canceled) {
		reportRunError(mt, err)
	}

	mt.mu.Lock()
	mt.running = false
	mt.cancel = nil
	mt.lastErr = err
	mt.spendUSD += outcome.HostedSpendUSD

	if mt.baseline == "" {
		mt.baseline = outcome.Checkpoint
	}

	mt.mu.Unlock()
}

// runCycle drives the thread's cycle instead of one loop. The thread's own
// log carries the phase transitions and Condition verdicts, and each phase's
// model work runs in a thread of its own, so what crosses a phase boundary
// is the standing goal, the change set, and the ledger rather than the
// transcript.
func (m *manager) runCycle(ctx context.Context, mt *managedThread, route router.Input, prompt string) {
	err := m.driveCycle(ctx, mt, route, prompt)

	mt.mu.Lock()
	mt.running = false
	mt.cancel = nil
	mt.lastErr = err
	mt.mu.Unlock()
}

func (m *manager) driveCycle(ctx context.Context, mt *managedThread, route router.Input, prompt string) error {
	c, err := m.cycles.Cycle(mt.cycle)
	if err != nil {
		return fmt.Errorf("resolving cycle %s: %w", mt.cycle, err)
	}

	if err := mt.th.SetState(ctx, event.StateWorking); err != nil {
		return fmt.Errorf("setting state: %w", err)
	}

	driver := m.cycles.CycleDriver(mt.th.ID(), mt.dirs, route)

	outcome, err := cycle.NewRunner(firstDir(mt.dirs), driver, mt.th.Log()).Run(ctx, c, prompt)

	m.toolCalls.Add(int64(outcome.ToolCalls))
	m.spend.add(outcome.SpendUSD)

	mt.mu.Lock()
	mt.spendUSD += outcome.SpendUSD
	mt.mu.Unlock()

	if err != nil {
		return m.failCycle(ctx, mt, err)
	}

	return m.finishCycle(ctx, mt, outcome)
}

// failCycle records a cycle whose phase could not run at all, which is a
// broken run rather than a harness refusal.
func (*manager) failCycle(ctx context.Context, mt *managedThread, cause error) error {
	if _, logErr := mt.th.Log().Append(event.Event{
		Kind: event.KindError, Text: "cycle stopped: " + cause.Error(),
	}); logErr != nil {
		return logErr //nolint:wrapcheck // the log error replaces the cause it could not record
	}

	if err := mt.th.SetState(ctx, event.StateFailed); err != nil {
		return fmt.Errorf("setting state: %w", err)
	}

	return cause
}

// finishCycle records where the cycle ended. A cycle whose last phase's
// Condition did not hold is failed, never done: reporting it as finished
// would put the model's account of itself where a check belongs.
func (*manager) finishCycle(ctx context.Context, mt *managedThread, outcome cycle.Outcome) error {
	state := event.StateFailed
	if outcome.Stop == cycle.StopComplete {
		state = event.StateDone
	}

	if err := mt.th.SetState(ctx, state); err != nil {
		return fmt.Errorf("setting state: %w", err)
	}

	return nil
}

// reportRunError puts a run that failed before the loop could describe it
// (a checkpoint that could not be captured, a thread I/O error) on the
// thread's own log, so every client sees the reason instead of an idle
// thread with an empty transcript.
func reportRunError(mt *managedThread, err error) {
	log := mt.th.Log()
	if _, aerr := log.Append(event.Event{Kind: event.KindState, State: event.StateFailed}); aerr != nil {
		slog.Warn("recording run failure state", "thread", mt.id, "err", aerr)
	}
	if _, aerr := log.Append(event.Event{Kind: event.KindError, Text: err.Error()}); aerr != nil {
		slog.Warn("recording run error", "thread", mt.id, "err", aerr)
	}
}

// admit holds a turn until the machine has room for the local model beside
// whatever else is running. A turn pinned hosted skips admission: it occupies
// no local memory, and holding it back would trade a network call for a wait.
func (m *manager) admit(ctx context.Context, threadID string, override router.Choice) (func(), error) {
	if override == router.ChoiceHosted {
		return func() {}, nil
	}

	release, err := m.scheduler.AdmitTurn(ctx, threadID)
	if err != nil {
		return nil, fmt.Errorf("admitting a turn on %s: %w", threadID, err)
	}

	return release, nil
}

// expand resolves the prompt's mentions, logging each that did not so the
// user sees the reference went nowhere. An expansion that fails leaves the
// prompt as typed rather than losing the turn.
func (m *manager) expand(ctx context.Context, mt *managedThread, prompt string) string {
	if m.mentions == nil {
		return prompt
	}

	res, err := m.mentions.Expand(ctx, prompt)
	if err != nil {
		return prompt
	}

	for _, um := range res.Unresolved() {
		ev := event.Event{Kind: event.KindError, Text: "@" + um.Ref + " did not resolve: " + um.Detail}
		if _, logErr := mt.th.Log().Append(ev); logErr != nil {
			return res.Prompt
		}
	}

	return res.Prompt
}

// Differ produces a thread's change set as unified diff text. The jj
// backend satisfies it, and the daemon takes an interface so a Server
// without a repository still runs.
type Differ interface {
	Diff(ctx context.Context, repoRoot, marker string, files []string) (string, error)
}

// diff returns threadID's changes since its first turn began. A thread that
// has not run yet has no baseline and so no diff, which is not an error.
func (m *manager) diff(ctx context.Context, differ Differ, threadID string) (string, error) {
	mt, ok := m.get(threadID)
	if !ok {
		return "", ErrThreadNotFound
	}

	mt.mu.Lock()
	baseline, dir := mt.baseline, firstDir(mt.dirs)
	mt.mu.Unlock()

	if baseline == "" || differ == nil || dir == "" {
		return "", nil
	}

	out, err := differ.Diff(ctx, dir, baseline, nil)
	if err != nil {
		return "", fmt.Errorf("diffing thread %s: %w", threadID, err)
	}

	return out, nil
}

// Restorer reverts a directory to a captured operation and reports what
// that costs. The jj backend satisfies it, and the daemon takes an
// interface so a Server without a repository still runs.
type Restorer interface {
	ChangedFiles(ctx context.Context, repoRoot, marker string) ([]string, error)
	DiffStat(ctx context.Context, repoRoot, marker string) (string, error)
	Restore(ctx context.Context, repoRoot, checkpoint string) error
}

// restore previews or performs an undo of threadID back to the checkpoint
// captured before its first turn. A preview reports the work the restore
// would discard; only confirm actually destroys it.
//
// Nothing to discard is an error rather than a successful no-op, so a
// client never reports an undo that undid nothing.
func (m *manager) restore(ctx context.Context, r Restorer, threadID string, confirm bool) (api.Restore, error) {
	mt, ok := m.get(threadID)
	if !ok {
		return api.Restore{}, ErrThreadNotFound
	}

	mt.mu.Lock()
	baseline, dir, running := mt.baseline, firstDir(mt.dirs), mt.running
	mt.mu.Unlock()

	switch {
	case running:
		return api.Restore{}, ErrThreadBusy
	case baseline == "":
		return api.Restore{}, ErrNoCheckpoint
	case r == nil || dir == "":
		return api.Restore{}, ErrNoRepository
	}

	changed, err := r.ChangedFiles(ctx, dir, baseline)
	if err != nil {
		return api.Restore{}, fmt.Errorf("listing what thread %s would discard: %w", threadID, err)
	}
	if len(changed) == 0 {
		return api.Restore{}, ErrNothingToRestore
	}

	summary, err := r.DiffStat(ctx, dir, baseline)
	if err != nil {
		return api.Restore{}, fmt.Errorf("summarizing what thread %s would discard: %w", threadID, err)
	}

	out := api.Restore{ThreadID: threadID, Checkpoint: baseline, Summary: summary}
	if !confirm {
		return out, nil
	}

	if err := performRestore(ctx, r, dir, baseline); err != nil {
		return api.Restore{}, err
	}

	out.Restored = true

	return out, nil
}

// performRestore reverts dir and proves it: jj reports "Nothing changed"
// through a zero exit status, so a restore is only believable once the
// working copy stops differing from the checkpoint.
func performRestore(ctx context.Context, r Restorer, dir, checkpoint string) error {
	if err := r.Restore(ctx, dir, checkpoint); err != nil {
		return fmt.Errorf("restoring %s: %w", dir, err)
	}

	left, err := r.ChangedFiles(ctx, dir, checkpoint)
	if err != nil {
		return fmt.Errorf("verifying the restore of %s: %w", dir, err)
	}
	if len(left) > 0 {
		return fmt.Errorf("%w: %d file(s) still differ", ErrRestoreIncomplete, len(left))
	}

	return nil
}

// cancel stops threadID's in-flight turn, if any. It is a no-op when the
// thread is idle.
func (m *manager) cancel(threadID string) error {
	mt, ok := m.get(threadID)
	if !ok {
		return ErrThreadNotFound
	}

	mt.mu.Lock()
	cancel := mt.cancel
	mt.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	return nil
}

// waitIdle waits for every thread's in-flight turn to finish, canceling
// anything still running once ctx is done.
func (m *manager) waitIdle(ctx context.Context) {
	for _, mt := range m.snapshot() {
		mt.mu.Lock()
		done, cancel := mt.done, mt.cancel
		mt.mu.Unlock()
		if done == nil {
			continue
		}

		select {
		case <-done:
		case <-ctx.Done():
			if cancel != nil {
				cancel()
			}
			<-done
		}
	}
}

// closeAll stops every thread's watch goroutine and flushes and closes its
// event log.
func (m *manager) closeAll() {
	threads := m.snapshot()
	m.cancelAll()

	for _, mt := range threads {
		if err := mt.th.Close(); err != nil {
			mt.mu.Lock()
			mt.lastErr = err
			mt.mu.Unlock()
		}
	}
}

// clampRows narrows an event log's head to the int the wire carries. A log
// long enough to overflow one has other problems, and reporting a negative
// row count is not the way to surface them.
func clampRows(head uint64) int {
	if head > math.MaxInt {
		return math.MaxInt
	}

	return int(head)
}
