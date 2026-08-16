package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

// ErrNoThreadContext reports a question Ask call whose context was not
// produced by the daemon's own turn runner, so no thread ID is available to
// address the pending prompt to.
var ErrNoThreadContext = errors.New("daemon: no thread in context")

type threadIDKey struct{}

func withThreadID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, id)
}

func threadIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(threadIDKey{}).(string)

	return id, ok
}

type pendingItem struct {
	ch   chan api.Command
	info api.PendingInfo
}

// Broker turns a permission request or a question from any thread into an
// api.PendingInfo answerable from any connected client, and resolves the
// waiting Gate or Asker call exactly once no matter how many clients race to
// answer it. It is safe for concurrent use.
type Broker struct {
	onChange func()
	mgr      *manager
	items    map[string]*pendingItem
	asked    atomic.Int64
	denied   atomic.Int64
	mu       sync.Mutex
}

// NewBroker builds an unattached Broker. Pass its Gate and Asker into
// whatever builds the shared agent.Loop and tool.Registry, then pass the
// Broker itself to daemon.New via WithBroker so answers reach it.
func NewBroker() *Broker {
	return &Broker{items: make(map[string]*pendingItem)}
}

func (b *Broker) attach(mgr *manager, onChange func()) {
	b.mu.Lock()
	b.mgr = mgr
	b.onChange = onChange
	b.mu.Unlock()
}

// PermissionGate adapts a Broker to permission.Gate.
type PermissionGate struct{ b *Broker }

// Ask implements permission.Gate by registering req as a pending prompt and
// blocking until a client answers it or ctx is done.
func (g PermissionGate) Ask(ctx context.Context, req permission.Request) (permission.Decision, error) {
	g.b.asked.Add(1)

	cmd, err := g.b.wait(ctx, req.ThreadID, api.PendingInfo{
		Tool:   req.Tool,
		Action: req.Action,
		Detail: req.Detail,
		Stakes: req.Stakes,
	})
	if err != nil {
		g.b.denied.Add(1)

		return permission.Deny, err
	}
	if cmd.Decision == permission.Deny {
		g.b.denied.Add(1)
	}

	return cmd.Decision, nil
}

// Gate returns the permission.Gate every thread's turns should ask through.
func (b *Broker) Gate() PermissionGate { return PermissionGate{b: b} }

// QuestionAsker adapts a Broker to the internal/tools.Asker interface
// (Ask(ctx, string) (string, error)) structurally, without daemon importing
// that package.
type QuestionAsker struct{ b *Broker }

// Ask registers question as a pending prompt against the thread named in
// ctx and blocks until a client answers it or ctx is done.
func (a QuestionAsker) Ask(ctx context.Context, question string) (string, error) {
	threadID, ok := threadIDFromContext(ctx)
	if !ok {
		return "", ErrNoThreadContext
	}

	cmd, err := a.b.wait(ctx, threadID, api.PendingInfo{Action: "question", Detail: question, Question: true})
	if err != nil {
		return "", err
	}

	return cmd.Answer, nil
}

// Asker returns the question-answering interface for tools.NewQuestion.
func (b *Broker) Asker() QuestionAsker { return QuestionAsker{b: b} }

func (b *Broker) wait(ctx context.Context, threadID string, info api.PendingInfo) (api.Command, error) {
	info.ID = newID()
	info.ThreadID = threadID
	info.Asked = time.Now()

	b.mu.Lock()
	mgr := b.mgr
	b.mu.Unlock()
	if mgr != nil {
		if mt, ok := mgr.get(threadID); ok {
			mt.mu.Lock()
			info.Thread, info.Dir = mt.name, firstDir(mt.dirs)
			mt.mu.Unlock()
		}
	}

	item := &pendingItem{info: info, ch: make(chan api.Command, 1)}
	b.mu.Lock()
	b.items[info.ID] = item
	b.mu.Unlock()

	b.setState(threadID, event.StateNeedsIn)
	b.fireChange()

	defer func() {
		b.mu.Lock()
		delete(b.items, info.ID)
		b.mu.Unlock()
		b.setState(threadID, event.StateWorking)
		b.fireChange()
	}()

	select {
	case cmd := <-item.ch:
		return cmd, nil
	case <-ctx.Done():
		return api.Command{}, fmt.Errorf("waiting for answer: %w", ctx.Err())
	}
}

// Answer resolves the pending prompt named by cmd.PromptID, reporting
// whether one was found. Two callers racing to answer the same prompt: only
// the one that wins the map delete resolves it, so it settles exactly once.
func (b *Broker) Answer(cmd api.Command) bool {
	b.mu.Lock()
	item, ok := b.items[cmd.PromptID]
	if ok {
		delete(b.items, cmd.PromptID)
	}
	b.mu.Unlock()
	if !ok {
		return false
	}

	item.ch <- cmd
	close(item.ch)
	b.fireChange()

	return true
}

// List returns every pending prompt across every thread, oldest first.
func (b *Broker) List() []api.PendingInfo {
	b.mu.Lock()
	out := make([]api.PendingInfo, 0, len(b.items))
	for _, it := range b.items {
		out = append(out, it.info)
	}
	b.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Asked.Before(out[j].Asked) })

	return out
}

func (b *Broker) gateQueueCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := 0
	for _, it := range b.items {
		if !it.info.Question {
			n++
		}
	}

	return n
}

func (b *Broker) askedCount() int  { return int(b.asked.Load()) }
func (b *Broker) deniedCount() int { return int(b.denied.Load()) }

func (b *Broker) setState(threadID string, state event.State) {
	b.mu.Lock()
	mgr := b.mgr
	b.mu.Unlock()
	if mgr == nil {
		return
	}
	if err := mgr.appendState(threadID, state); err != nil {
		return
	}
}

func (b *Broker) fireChange() {
	b.mu.Lock()
	cb := b.onChange
	b.mu.Unlock()
	if cb != nil {
		cb()
	}
}
