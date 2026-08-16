package gate_test

import (
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// fakeClock is gate.Clock driven entirely by Advance, so a test never
// sleeps to exercise debounce or cadence logic.
type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
	mu     sync.Mutex
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

//nolint:ireturn // implements gate.Clock's Timer-returning contract
func (c *fakeClock) NewTimer(d time.Duration) gate.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()

	t := &fakeTimer{clock: c, deadline: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)

	return t
}

// Advance moves the clock forward by d and fires every timer whose
// deadline that reaches.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now

	var due []*fakeTimer

	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.deadline.After(now) {
			t.fired = true

			due = append(due, t)
		}
	}
	c.mu.Unlock()

	for _, t := range due {
		t.ch <- now
	}
}

type fakeTimer struct {
	clock    *fakeClock
	deadline time.Time
	ch       chan time.Time
	stopped  bool
	fired    bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	wasActive := !t.stopped && !t.fired
	t.stopped = true

	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	wasActive := !t.stopped && !t.fired
	t.stopped = false
	t.fired = false
	t.deadline = t.clock.now.Add(d)

	return wasActive
}
