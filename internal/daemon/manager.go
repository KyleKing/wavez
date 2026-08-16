package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
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
	threads   map[string]*managedThread
	logDir    string
	prefix    agent.Prefix
	order     []string
	toolCalls atomic.Int64
	malformed atomic.Int64
	mu        sync.Mutex
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
	}
}

// createParams describes a new thread.
type createParams struct {
	Model  string
	Parent string
	Dirs   []string
}

// create does not take a context: a thread outlives the request that
// created it, so its lifetime is m.ctx (the manager's), never a caller's.
func (m *manager) create(p createParams) (*managedThread, error) {
	id := newID()

	var opts []thread.Option
	if p.Model != "" {
		opts = append(opts, thread.WithModel(p.Model))
	}
	if p.Parent != "" {
		opts = append(opts, thread.WithParent(thread.ID(p.Parent)))
	}

	th, err := thread.Open(m.logDir, thread.ID(id), p.Dirs, opts...)
	if err != nil {
		return nil, fmt.Errorf("opening thread: %w", err)
	}

	mt := &managedThread{
		th:      th,
		id:      id,
		dirs:    p.Dirs,
		model:   p.Model,
		parent:  p.Parent,
		created: time.Now(),
		state:   event.StateIdle,
	}

	go mt.watch(m.ctx)

	m.mu.Lock()
	m.threads[id] = mt
	m.order = append(m.order, id)
	m.mu.Unlock()

	return mt, nil
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

	mt.mu.Lock()
	mt.running = false
	mt.cancel = nil
	mt.lastErr = err
	mt.mu.Unlock()
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
