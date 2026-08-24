package tools

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrOutOfScope reports an edit to a file the run has neither read nor
// created.
var ErrOutOfScope = errors.New("file was never read or created by this run")

// Scope records which files a run has looked at, so an edit to a file it
// never opened can be told apart from an ordinary one.
//
// The project root is not a useful fence for this. Root containment is a
// sandbox boundary, and it passes every edit anywhere inside the repo,
// which is how an unattended run came to rewrite files it had never opened
// while nominally creating one. Reading a file first is what a deliberate
// edit looks like, so that is what gets tracked.
//
// Strayed edits are recorded rather than refused. Refusing is one
// constructor argument away, and stays off until there is evidence about
// what legitimate runs actually touch, because a fence that blocks real
// work gets disabled wholesale and then protects nothing.
type Scope struct {
	seen    map[string]bool
	strayed map[string]bool
	// readAt and wroteAt order a path's last read against its last accepted
	// write, so a stale anchor can be told apart from a wrong one. A model
	// writes its next anchor from the file it read, and after its own edit
	// that file no longer exists anywhere.
	readAt  map[string]int
	wroteAt map[string]int
	clock   int
	mu      sync.Mutex
	strict  bool
}

// NewScope builds a Scope. A strict Scope refuses an out-of-scope edit; a
// permissive one records it and allows it.
func NewScope(strict bool) *Scope {
	return &Scope{
		seen: map[string]bool{}, strayed: map[string]bool{},
		readAt: map[string]int{}, wroteAt: map[string]int{}, strict: strict,
	}
}

// Observe brings abs into scope, because the run has now read or created
// it.
func (s *Scope) Observe(abs string) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.seen[abs] = true
	s.clock++
	s.readAt[abs] = s.clock
}

// Wrote records an edit that landed, which is what makes a later anchor
// against the caller's own memory of the file stale. It is called after the
// write rather than before it, because a refused edit changed nothing.
func (s *Scope) Wrote(abs string) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clock++
	s.wroteAt[abs] = s.clock
}

// Stale reports whether this run wrote abs after it last read it, so an
// anchor drawn from that read cannot match.
func (s *Scope) Stale(abs string) bool {
	if s == nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.wroteAt[abs] > s.readAt[abs]
}

// Edit reports whether the run may edit abs, recording the path when it is
// out of scope. A permissive Scope always returns nil and still records.
func (s *Scope) Edit(abs string) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.seen[abs] {
		return nil
	}

	s.strayed[abs] = true

	if s.strict {
		return fmt.Errorf("%w: %s", ErrOutOfScope, abs)
	}

	return nil
}

// Strayed returns the files edited out of scope, sorted. The list is what a
// client reports at the end of a run.
func (s *Scope) Strayed() []string {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.strayed))
	for path := range s.strayed {
		out = append(out, path)
	}

	sort.Strings(out)

	return out
}
