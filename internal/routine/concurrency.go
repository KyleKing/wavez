package routine

import (
	"context"
	"sync"
)

// waiter is one run queued behind a busy concurrency key.
type waiter struct {
	ready   chan struct{}
	routine string
}

// group is one concurrency key's state: at most one run holds it, and the
// rest wait in a queue whose service order is the arriving run's strategy.
type group struct {
	cancelHeld context.CancelFunc
	// lastServed is the routine admitted most recently, which is what makes
	// round-robin rotate rather than repeat.
	lastServed string
	waiters    []*waiter
	mu         sync.Mutex
	held       bool
	// rotate is set once a routine declaring round-robin has queued on this
	// key, and is what makes next pick fairly instead of first-in-first-out.
	rotate bool
}

// groups owns one group per concurrency key.
type groups struct {
	byKey map[string]*group
	mu    sync.Mutex
}

func newGroups() *groups { return &groups{byKey: make(map[string]*group)} }

func (g *groups) get(key string) *group {
	g.mu.Lock()
	defer g.mu.Unlock()

	grp, ok := g.byKey[key]
	if !ok {
		grp = &group{}
		g.byKey[key] = grp
	}

	return grp
}

// acquire takes rt's concurrency key and returns the context its run must
// use, which a later cancel-in-progress run cancels. The release func hands
// the key to the next waiter.
func (g *groups) acquire(ctx context.Context, rt *Routine) (context.Context, func(), error) {
	grp := g.get(rt.key())

	w, runCtx, cancel, admitted := grp.enter(ctx, rt)
	if admitted {
		return runCtx, func() { grp.leave(cancel) }, nil
	}

	select {
	case <-w.ready:
		runCtx, cancel := grp.take(ctx, rt)

		return runCtx, func() { grp.leave(cancel) }, nil
	case <-ctx.Done():
		grp.drop(w)

		// The caller reports the run canceled; wrapping the context error
		// reads no better at the surface.
		return nil, nil, ctx.Err() //nolint:wrapcheck // see above
	}
}

// enter admits rt outright when the key is free, and otherwise queues it,
// canceling the run in progress first when that is the arriving run's
// strategy.
func (g *group) enter(ctx context.Context, rt *Routine) (*waiter, context.Context, context.CancelFunc, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.held {
		runCtx, cancel := context.WithCancel(ctx)
		g.held = true
		g.cancelHeld = cancel
		g.lastServed = rt.Name

		return nil, runCtx, cancel, true
	}

	w := &waiter{routine: rt.Name, ready: make(chan struct{})}
	g.waiters = append(g.waiters, w)

	if rt.Concurrency == RoundRobin {
		g.rotate = true
	}

	if rt.Concurrency == CancelInProgress && g.cancelHeld != nil {
		g.cancelHeld()
	}

	return w, nil, nil, false
}

// take records an admitted waiter as the key's holder.
func (g *group) take(ctx context.Context, rt *Routine) (context.Context, context.CancelFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	g.cancelHeld = cancel
	g.lastServed = rt.Name

	return runCtx, cancel
}

// leave releases the key and admits the next waiter.
func (g *group) leave(cancel context.CancelFunc) {
	cancel()

	g.mu.Lock()
	defer g.mu.Unlock()

	g.cancelHeld = nil

	next := g.next()
	if next < 0 {
		g.held = false

		return
	}

	w := g.waiters[next]
	g.waiters = append(g.waiters[:next], g.waiters[next+1:]...)
	close(w.ready)
}

// next picks the waiter to admit: the queue is FIFO, except that a waiter
// from a routine other than the one just served jumps ahead, which is the
// rotation round-robin buys and the reason one routine firing in a loop
// cannot starve another on the same key.
func (g *group) next() int {
	if len(g.waiters) == 0 {
		return -1
	}

	if !g.rotate {
		return 0
	}

	for i, w := range g.waiters {
		if w.routine != g.lastServed {
			return i
		}
	}

	return 0
}

func (g *group) drop(w *waiter) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i, have := range g.waiters {
		if have == w {
			g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)

			return
		}
	}
}
