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

// RunScope hands out the identity of the run in progress. A gate is handed a
// batch's change set and nothing about who produced it, so per-run state had
// nowhere to live: the lint gate could not tell a finding the run inherited
// from one the run caused, and a baseline recorded for one run would have been
// read by the next. A RunScope is that identity. Begin starts a new run and
// Current names the one in progress; the caller that knows where a run starts
// (the agent loop, between runs) is the one that calls Begin.
//
// A nil *RunScope reports "" from both methods, so a caller that never wired
// one stays on exactly the behavior it had before run identity existed.
type RunScope struct {
	id string
	mu sync.Mutex
}

// NewRunScope builds a scope with no run in progress.
func NewRunScope() *RunScope { return &RunScope{} }

// Begin starts a new run, forgetting the previous one, and returns its
// identity. Anything recorded under the old identity stops being handed out.
func (s *RunScope) Begin() string {
	if s == nil {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.id = fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), runCounter.Add(1))

	return s.id
}

// Current names the run in progress, "" when none has begun.
func (s *RunScope) Current() string {
	if s == nil {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.id
}
