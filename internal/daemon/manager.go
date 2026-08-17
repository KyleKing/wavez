package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

// Sentinel errors manager operations return.
var (
	ErrThreadNotFound = errors.New("daemon: thread not found")
	ErrThreadBusy     = errors.New("daemon: thread is already running a turn")
)

// manager holds every live thread for the lifetime of the Server, so a
// thread survives a client disconnecting and reconnecting.
type manager struct {
	ctx       context.Context //nolint:containedctx // scopes every thread's lifetime to the manager
	loop      *agent.Loop
	cancelAll context.CancelFunc
	spend     *spendLedger
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

	go mt.watch(m.ctx)

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

// accumulatedChanges collapses a thread's log to one entry per changed
// file, keeping the last line ranges recorded for it.
func accumulatedChanges(mt *managedThread) ([]tool.Change, error) {
	events, err := mt.th.Log().Since(0)
	if err != nil {
		return nil, fmt.Errorf("reading thread %s: %w", mt.id, err)
	}

	byPath := map[string]tool.Change{}

	var order []string

	for i := range events {
		for _, c := range events[i].Changes {
			if _, seen := byPath[c.Path]; !seen {
				order = append(order, c.Path)
			}

			byPath[c.Path] = c
		}
	}

	out := make([]tool.Change, 0, len(order))
	for _, path := range order {
		out = append(out, byPath[path])
	}

	return out, nil
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

func (m *manager) needsInputCount() int {
	n := 0
	for _, mt := range m.snapshot() {
		if mt.currentState() == event.StateNeedsIn {
			n++
		}
	}

	return n
}

// totalUsage sums every live thread's token counts. The context field is left
// zero: an occupied window belongs to one thread and does not add up across
// them.
func (m *manager) totalUsage() usage {
	var out usage

	for _, mt := range m.snapshot() {
		mt.mu.Lock()
		u := mt.usage
		mt.mu.Unlock()

		out.input += u.input
		out.output += u.output
		out.cacheRead += u.cacheRead
	}

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

	runCtx := withThreadID(ctx, mt.id)
	outcome, err := m.loop.Run(runCtx, mt.th, m.prefix, prompt, router.Input{})

	m.toolCalls.Add(int64(outcome.ToolCalls))
	if outcome.Stop == agent.StopMalformedTool {
		m.malformed.Add(1)
	}
	m.spend.add(outcome.HostedSpendUSD)

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
