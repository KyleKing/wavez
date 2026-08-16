package gate

import "time"

// Clock abstracts time so the debounce and cadence logic never sleeps in
// tests: a fake Clock advances on command instead of wall time passing.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer abstracts a single-shot timer so Runner's reset-on-activity
// debounce works against both the real clock and a fake one.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// RealClock is Clock backed by the standard library.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time {
	return time.Now()
}

// NewTimer starts a real time.Timer.
//
//nolint:ireturn // Clock's contract is the Timer interface, not a concrete type
func (RealClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
