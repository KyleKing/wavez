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

// threadLookup finds a thread's cached info and records a state transition
// on its log, by id alone: a Broker knows a pending prompt's thread id, not
// which project loaded it. *Server satisfies this by routing through its
// thread index.
type threadLookup interface {
	get(id string) (*managedThread, bool)
	appendState(id string, state event.State) error
	// park and unpark give back and reclaim a thread's turn admission
	// around a Broker wait, so a thread blocked on a human does not also
	// hold memory admission a working thread needs.
	park(id string) error
	unpark(ctx context.Context, id string) error
}

// Broker turns a permission request or a question from any thread into an
// api.PendingInfo answerable from any connected client, and resolves the
// waiting Gate or Asker call exactly once no matter how many clients race to
// answer it. It is safe for concurrent use.
type Broker struct {
	onChange func()
	lookup   threadLookup
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

func (b *Broker) attach(lookup threadLookup, onChange func()) {
	b.mu.Lock()
	b.lookup = lookup
	b.onChange = onChange
	b.mu.Unlock()
}

// PermissionGate adapts a Broker to permission.Gate.
type PermissionGate struct{ b *Broker }

// Ask implements permission.Gate by registering req as a pending prompt and
// blocking until a client answers it or ctx is done.
func (g PermissionGate) Ask(ctx context.Context, req permission.Request) (permission.Decision, error) {
	g.b.asked.Add(1)

	// The daemon's own turn context carries the thread id (set once, in
	// manager.runTurn); req.ThreadID exists for a caller outside that flow
	// and is used only when ctx carries none.
	threadID := req.ThreadID
	if id, ok := threadIDFromContext(ctx); ok {
		threadID = id
	}

	cmd, err := g.b.wait(ctx, threadID, api.PendingInfo{
		Tool:   req.Tool,
		Action: req.Action,
		Detail: req.Detail,
		Reason: req.Reason,
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
	lookup := b.lookup
	b.mu.Unlock()
	if lookup != nil {
		if mt, ok := lookup.get(threadID); ok {
			mt.mu.Lock()
			// Step is what the thread was doing just before this prompt
			// parked it, captured ahead of the state transition below,
			// which would otherwise overwrite it with "needs input".
			info.Thread, info.Dir, info.Step = mt.name, firstDir(mt.dirs), mt.step
			mt.mu.Unlock()
		}
	}

	item := &pendingItem{info: info, ch: make(chan api.Command, 1)}
	b.mu.Lock()
	b.items[info.ID] = item
	b.mu.Unlock()

	b.setState(threadID, event.StateNeedsIn)
	parkThread(lookup, threadID)
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
		if err := unparkThread(ctx, lookup, threadID); err != nil {
			return api.Command{}, err
		}

		return cmd, nil
	case <-ctx.Done():
		return api.Command{}, fmt.Errorf("waiting for answer: %w", ctx.Err())
	}
}

// parkThread gives back threadID's turn admission for the duration of the
// wait, so the scheduler can admit a thread that is not blocked on a human.
// It is best-effort: a lookup-less Broker (a test built without one) parks
// nothing, and a thread the lookup no longer knows about (already gone) has
// nothing left to release.
func parkThread(lookup threadLookup, threadID string) {
	if lookup == nil {
		return
	}

	if err := lookup.park(threadID); err != nil {
		return
	}
}

// unparkThread re-admits threadID before the turn that was waiting on this
// prompt continues, blocking on the same terms as any other admission.
func unparkThread(ctx context.Context, lookup threadLookup, threadID string) error {
	if lookup == nil {
		return nil
	}

	return lookup.unpark(ctx, threadID)
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
	lookup := b.lookup
	b.mu.Unlock()
	if lookup == nil {
		return
	}
	if err := lookup.appendState(threadID, state); err != nil {
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
