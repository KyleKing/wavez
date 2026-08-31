package tools

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/kyleking/wavez/internal/guard"
)

// ErrOutOfScope reports an edit to a file the run has neither read nor
// created.
var ErrOutOfScope = errors.New("file was never read or created by this run")

// ErrProtected reports an edit to a file that decides what a later command
// may run. Unlike ErrOutOfScope it holds in every mode, because approving
// it would be approving every command it goes on to permit.
var ErrProtected = errors.New("this file " + guard.ReasonProtected)

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
	// origin holds each file's bytes as they were before this run first
	// wrote it, which is the only thing a run may undo. Reverting further
	// than that would discard work this run never made.
	origin map[string]origin
	root   string
	clock  int
	mu     sync.Mutex
	strict bool
}

// NewScope builds a Scope over the project rooted at root. A strict Scope
// refuses an out-of-scope edit; a permissive one records it and allows it.
// Both refuse a protected path.
func NewScope(root string, strict bool) *Scope {
	return &Scope{
		seen: map[string]bool{}, strayed: map[string]bool{},
		readAt: map[string]int{}, wroteAt: map[string]int{},
		origin: map[string]origin{}, root: root, strict: strict,
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

// An origin is a file as it stood before this run first wrote it, where
// existed separates a file the run created, which an undo removes, from
// one it found empty.
type origin struct {
	src     []byte
	existed bool
}

// snapshotOnce reads abs and keeps it if this run has not written it yet.
// A file it cannot read is one that does not exist, which an undo restores
// by removing what the run created.
func (s *Scope) snapshotOnce(abs string) {
	if _, _, ok := s.Origin(abs); ok {
		return
	}

	src, err := os.ReadFile(abs) //nolint:gosec // the caller's own resolved edit target
	s.snapshot(abs, origin{src: src, existed: err == nil})
}

// snapshot keeps the first state seen for abs, so an undo returns to what
// the run inherited rather than to its own previous edit.
func (s *Scope) snapshot(abs string, o origin) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.origin[abs]; !ok {
		s.origin[abs] = o
	}
}

// Origin returns abs as this run first found it, and whether it existed
// then. The second result is false for a file the run has never edited.
func (s *Scope) Origin(abs string) ([]byte, bool, bool) {
	if s == nil {
		return nil, false, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.origin[abs]

	return o.src, o.existed, ok
}

// Read reports whether this run has ever read or created abs. An anchor
// into a file it has not is text the caller got from somewhere else, and
// the only other source is a search result, which is trimmed matched lines
// rather than the file.
func (s *Scope) Read(abs string) bool {
	if s == nil {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readAt[abs] > 0
}

// Stale reports whether this run wrote abs after it last read it, so an
// anchor drawn from that read cannot match.
func (s *Scope) Stale(abs string) bool {
	if s == nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// A file this run has never read is not stale, it is unread, and the
	// two want different advice.
	return s.readAt[abs] > 0 && s.wroteAt[abs] > s.readAt[abs]
}

// Edit reports whether the run may edit abs, recording the path when it is
// out of scope. A permissive Scope always returns nil and still records.
//
// It snapshots abs on the way through, because every tool that writes calls
// this first and none of them can hand back the bytes afterwards.
func (s *Scope) Edit(abs string) error {
	if s == nil {
		return nil
	}

	if err := s.Protected(abs); err != nil {
		return err
	}

	s.snapshotOnce(abs)

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

// Protected reports whether abs is one of the files that decide what a
// later command may run without asking, which no tool may write in any
// mode. Tools that create a file rather than edit one call it directly,
// since they never reach Edit.
func (s *Scope) Protected(abs string) error {
	if s == nil {
		return nil
	}

	if where := guard.ProtectedWrite(s.root, abs); where != "" {
		return fmt.Errorf("%w (%s)", ErrProtected, where)
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
