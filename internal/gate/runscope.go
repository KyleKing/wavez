package gate

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// runCounter makes run identities unique within a process; the timestamp in
// newRunID makes them unique across processes.
var runCounter atomic.Uint64

// RunScope hands out the identity of each writer's run in progress. Per-run
// state had nowhere to live: the lint gate could not tell a finding the run
// inherited from one the run caused, and a baseline recorded for one run
// would have been read by the next. A RunScope is that identity, keyed by
// writer because one agent.Loop serves every thread, so a scope holding a
// single current run would hand a lane still working the identity of
// whichever lane started last.
//
// A nil *RunScope reports "" from both methods, so a caller that never wired
// one stays on exactly the behavior it had before run identity existed.
type RunScope struct {
	ids map[string]string
	mu  sync.Mutex
}

// NewRunScope builds a scope with no run in progress.
func NewRunScope() *RunScope { return &RunScope{ids: map[string]string{}} }

// Begin starts a new run for writer, forgetting that writer's previous one,
// and returns its identity. Anything recorded under the old identity stops
// being handed out. An empty writer begins nothing, since a run nobody wrote
// cannot be told apart from another.
func (s *RunScope) Begin(writer string) string {
	if s == nil || writer == "" {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), runCounter.Add(1))
	s.ids[writer] = id

	return id
}

// Current names the run writer has in progress, "" when it has begun none.
func (s *RunScope) Current(writer string) string {
	if s == nil || writer == "" {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.ids[writer]
}
