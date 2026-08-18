// Package lease coordinates concurrent writes between threads with advisory
// TTL leases over directory subtrees. A lease is keyed on the directory
// holding the write target rather than on the thread's working directory,
// because a thread writes wherever it likes: measured over 13,782 agent edits
// in _ai_/notes/agent-lock-coordination.md, 29% landed outside the session's
// own tree, and directory-level collisions ran 2.2x file-level.
package lease

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultTTL bounds how long a lease survives without being renewed. It is a
// backstop for a holder that died mid-write, which nothing else cleans up.
const DefaultTTL = 30 * time.Minute

// ErrNoHolder reports an acquisition whose context names no thread, so the
// lease could be attributed to nobody.
var ErrNoHolder = errors.New("lease: no holding thread in context")

// State grades a lease's collision risk.
type State string

// States a lease may hold.
const (
	// StateActive is a thread writing into the subtree right now. It is the
	// only state that makes another thread wait.
	StateActive State = "active"
	// StateCommitted is the weak signal left behind after the writes land:
	// the risk is no longer a concurrent edit, only a rebase.
	StateCommitted State = "committed"
	// StateExpired is a lease whose holder stopped renewing it, which is what
	// a crashed thread leaves behind.
	StateExpired State = "expired"
)

// Lease is one subtree's claim as a client renders it.
type Lease struct {
	Since   time.Time `json:"since"`
	Subtree string    `json:"subtree"`
	Holder  string    `json:"holder"`
	State   State     `json:"state"`
	Waiters []string  `json:"waiters,omitempty"`
}

// Wait reports a thread entering or leaving a wait on a lease, so a client
// can say what a stalled thread is stalled on.
type Wait struct {
	Holder  string
	Subtree string
	Blocker string
	Waiting bool
}

type holderKey struct{}

// WithHolder attaches the thread a write is attributed to. Tools read the
// holder from the context because one tool registry serves every thread.
func WithHolder(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, holderKey{}, threadID)
}

// HolderFrom reports the thread a write in ctx is attributed to.
func HolderFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(holderKey{}).(string)

	return id, ok
}

type entry struct {
	since   time.Time
	holder  string
	waiters []string
	depth   int
}

// Manager holds every live lease for one project. It is safe for concurrent
// use, and a nil *Manager is a working no-op so a caller with no coordination
// to do carries no branch for it.
type Manager struct {
	now     func() time.Time
	held    map[string]*entry
	onWait  func(Wait)
	wake    chan struct{}
	root    string
	ttl     time.Duration
	mu      sync.Mutex
	waiting int
}

// Option configures a Manager.
type Option func(*Manager)

// WithTTL sets how long a lease survives unrenewed.
func WithTTL(d time.Duration) Option {
	return func(m *Manager) { m.ttl = d }
}

// WithClock replaces the time source, for tests that must not sleep.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// OnWait registers the callback fired when a thread starts and stops waiting
// on a lease, which is how a client learns why a thread is idle. It runs on
// the waiting thread's goroutine and must not call back into the Manager.
func (m *Manager) OnWait(fn func(Wait)) {
	m.mu.Lock()
	m.onWait = fn
	m.mu.Unlock()
}

// New builds a Manager keying subtrees relative to root.
func New(root string, opts ...Option) *Manager {
	m := &Manager{
		root: root,
		ttl:  DefaultTTL,
		now:  time.Now,
		held: map[string]*entry{},
		wake: make(chan struct{}),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Subtree is the lease key for a write target: the directory holding it,
// relative to root. A target outside the project keys on its own absolute
// directory rather than on a path that climbs out of the root.
func Subtree(root, target string) string {
	dir := filepath.Dir(filepath.Clean(target))
	if root == "" {
		return dir
	}

	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return dir
	}

	return rel
}

// Overlaps reports whether two subtree keys contend, which covers the
// ancestor and descendant cases and not only an exact match.
func Overlaps(a, b string) bool {
	if a == b || a == "." || b == "." {
		return true
	}

	sep := string(filepath.Separator)

	return strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

// Acquire takes the lease covering target for the thread named in ctx,
// blocking while another thread writes an overlapping subtree, and returns
// the release func. Releasing downgrades the lease to committed rather than
// dropping it, so the subtree keeps its weak signal until the TTL runs out.
//
// The same thread may hold overlapping subtrees at once: a write inside a
// write would otherwise wait on itself.
func (m *Manager) Acquire(ctx context.Context, target string) (func(), error) {
	if m == nil {
		return func() {}, nil
	}

	holder, ok := HolderFrom(ctx)
	if !ok || holder == "" {
		return nil, ErrNoHolder
	}

	key := Subtree(m.root, target)
	waiting := false

	for {
		c := m.try(key, holder)
		if c.blocker == "" {
			if waiting {
				m.leaveWait(key, holder)
				m.notify(Wait{Holder: holder, Subtree: key, Waiting: false})
			}

			return func() { m.release(key, holder) }, nil
		}

		if !waiting {
			waiting = true

			m.enterWait(key, holder)
			m.notify(Wait{Holder: holder, Subtree: key, Blocker: c.blocker, Waiting: true})
		}

		select {
		case <-c.wake:
		case <-ctx.Done():
			m.leaveWait(key, holder)
			m.notify(Wait{Holder: holder, Subtree: key, Blocker: c.blocker, Waiting: false})

			return nil, fmt.Errorf("waiting for %s: %w", key, ctx.Err())
		}
	}
}

// contention is what stands between a caller and a lease: the thread in the
// way and the channel that closes when something releases. An empty blocker
// means the lease was taken.
type contention struct {
	wake    <-chan struct{}
	blocker string
}

// try takes the lease if nothing active contends, and otherwise reports what
// contends.
func (m *Manager) try(key, holder string) contention {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	m.sweep(now)

	for other, e := range m.held {
		if e.holder == holder || m.state(e, now) != StateActive || !Overlaps(key, other) {
			continue
		}

		return contention{wake: m.wake, blocker: e.holder}
	}

	e, ok := m.held[key]
	if !ok || e.holder != holder {
		e = &entry{holder: holder}
		if ok {
			e.waiters = m.held[key].waiters
		}

		m.held[key] = e
	}

	e.depth++
	e.since = now

	return contention{}
}

// release downgrades key back to committed once its last holder is done.
func (m *Manager) release(key, holder string) {
	m.mu.Lock()

	e, ok := m.held[key]
	if ok && e.holder == holder && e.depth > 0 {
		e.depth--
		e.since = m.now()
	}

	if !ok || e.depth > 0 {
		m.mu.Unlock()

		return
	}

	close(m.wake)
	m.wake = make(chan struct{})
	m.mu.Unlock()
}

func (m *Manager) enterWait(key, holder string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.held[key]
	if !ok {
		e = &entry{}
		m.held[key] = e
	}

	e.waiters = append(e.waiters, holder)
	m.waiting++
}

func (m *Manager) leaveWait(key, holder string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.held[key]
	if !ok {
		return
	}

	for i, w := range e.waiters {
		if w != holder {
			continue
		}

		e.waiters = append(e.waiters[:i], e.waiters[i+1:]...)
		m.waiting--

		break
	}
}

func (m *Manager) notify(w Wait) {
	m.mu.Lock()
	fn := m.onWait
	m.mu.Unlock()

	if fn != nil {
		fn(w)
	}
}

// sweep drops leases past their TTL with nobody waiting on them. The caller
// holds m.mu.
func (m *Manager) sweep(now time.Time) {
	for key, e := range m.held {
		if len(e.waiters) > 0 || e.depth > 0 || now.Sub(e.since) <= m.ttl {
			continue
		}

		delete(m.held, key)
	}
}

func (m *Manager) state(e *entry, now time.Time) State {
	switch {
	case e.depth > 0 && now.Sub(e.since) > m.ttl:
		return StateExpired
	case e.depth > 0:
		return StateActive
	default:
		return StateCommitted
	}
}

// List returns every lease, subtree order, with each one's waiters.
func (m *Manager) List() []Lease {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	m.sweep(now)

	out := make([]Lease, 0, len(m.held))

	for key, e := range m.held {
		if e.holder == "" && len(e.waiters) == 0 {
			continue
		}

		out = append(out, Lease{
			Subtree: key,
			Holder:  e.holder,
			State:   m.state(e, now),
			Since:   e.since,
			Waiters: append([]string(nil), e.waiters...),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Subtree < out[j].Subtree })

	return out
}

// Counts is the pair the diagnostics panel shows: leases held right now and
// threads waiting on one.
type Counts struct {
	Held    int
	Waiting int
}

// Counts reports leases active right now and threads waiting on one.
func (m *Manager) Counts() Counts {
	if m == nil {
		return Counts{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()

	var c Counts

	for _, e := range m.held {
		if m.state(e, now) == StateActive {
			c.Held++
		}
	}

	c.Waiting = m.waiting

	return c
}
