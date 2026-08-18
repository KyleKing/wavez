// Package sched admits the work that competes for this laptop's memory. Only
// one model fits in 16 GB, so a turn on the local tier and a gate run are
// rivals rather than neighbors: two llama-server processes on the same 6 GB
// model already OOM'd Metal, and a Go suite beside a loaded model is the same
// arithmetic one step down.
package sched

import (
	"context"
	"fmt"
	"sync"

	"github.com/kyleking/wavez/internal/sysinfo"
)

// DefaultHeadroom is the free-memory fraction at or above which a local turn
// and a gate run may overlap. Measured on this laptop, qwen3:8b loaded leaves
// ~31% free, which is enough for a Go suite, while gemma4:12b leaves 14-18%,
// which is not. The default sits between the two.
const DefaultHeadroom = 0.25

// Phase names what the scheduler is letting run, in DESIGN.md's two-phase
// shape. It is derived from what holds admission rather than set, so it can
// never disagree with what is actually running.
type Phase string

// Phases the scheduler reports.
const (
	// PhaseEdit is threads writing while gate runs queue.
	PhaseEdit Phase = "edit"
	// PhaseExecute is gates and routines running while edits wait for the
	// subtrees they touch.
	PhaseExecute Phase = "execute"
)

// Hold reports a thread entering or leaving an admission wait, so a client
// can say why a thread that is not working is also not doing anything.
type Hold struct {
	Holder string
	Reason string
	Held   bool
}

// Snapshot is the scheduler's state as the schedule view renders it.
type Snapshot struct {
	Phase      Phase
	Memory     sysinfo.Memory
	Headroom   float64
	LocalTurns int
	GateRuns   int
	// MemoryMeasured is false when the machine's memory could not be read, in
	// which case admission lets everything through rather than stalling on a
	// number it does not have.
	MemoryMeasured bool
}

// Scheduler serializes local turns against gate runs while memory is tight.
// A nil *Scheduler admits everything, so a caller with no memory pressure to
// manage carries no branch for it.
type Scheduler struct {
	mem      func(context.Context) (sysinfo.Memory, error)
	onHold   func(Hold)
	wake     chan struct{}
	headroom float64
	turns    int
	gates    int
	mu       sync.Mutex
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithHeadroom sets the free-memory fraction below which a local turn and a
// gate run stop overlapping.
func WithHeadroom(fraction float64) Option {
	return func(s *Scheduler) { s.headroom = fraction }
}

// WithMemory replaces the memory reading, for tests and for a caller that
// already samples the machine.
func WithMemory(fn func(context.Context) (sysinfo.Memory, error)) Option {
	return func(s *Scheduler) { s.mem = fn }
}

// OnHold registers the callback fired when a thread starts and stops waiting
// for admission, which is how a client says why a thread is held back. It
// runs on the waiting goroutine and must not call back into the Scheduler.
func (s *Scheduler) OnHold(fn func(Hold)) {
	s.mu.Lock()
	s.onHold = fn
	s.mu.Unlock()
}

// New builds a Scheduler reading real machine memory.
func New(opts ...Option) *Scheduler {
	s := &Scheduler{
		headroom: DefaultHeadroom,
		mem:      sysinfo.ReadMemory,
		wake:     make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// AdmitTurn admits one turn that will occupy the local model, blocking while
// a gate run holds the machine and memory is tight. The returned func gives
// the admission back.
func (s *Scheduler) AdmitTurn(ctx context.Context, holder string) (func(), error) {
	return s.admit(ctx, holder, true)
}

// AdmitGate admits one gate run, blocking while a local turn is in flight and
// memory is tight.
func (s *Scheduler) AdmitGate(ctx context.Context) (func(), error) {
	return s.admit(ctx, "", false)
}

func (s *Scheduler) admit(ctx context.Context, holder string, isTurn bool) (func(), error) {
	if s == nil {
		return func() {}, nil
	}

	held := false

	for {
		c := s.enter(ctx, isTurn)
		if c.reason == "" {
			if held {
				s.notify(Hold{Holder: holder, Held: false})
			}

			return func() { s.leave(isTurn) }, nil
		}

		if !held {
			held = true

			s.notify(Hold{Holder: holder, Reason: c.reason, Held: true})
		}

		select {
		case <-c.wake:
		case <-ctx.Done():
			s.notify(Hold{Holder: holder, Held: false})

			return nil, fmt.Errorf("waiting for admission: %w", ctx.Err())
		}
	}
}

// contention is what keeps a caller out: why it waits and the channel that
// closes when something finishes. An empty reason means it was admitted.
type contention struct {
	wake   <-chan struct{}
	reason string
}

// enter takes the admission if the machine has room, and otherwise reports
// what keeps the caller out.
func (s *Scheduler) enter(ctx context.Context, isTurn bool) contention {
	mem, measured := s.read(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	rival := s.gates
	if !isTurn {
		rival = s.turns
	}

	if measured && rival > 0 && free(mem) < s.headroom {
		return contention{wake: s.wake, reason: holdReason(mem, isTurn)}
	}

	if isTurn {
		s.turns++
	} else {
		s.gates++
	}

	return contention{}
}

func (s *Scheduler) leave(isTurn bool) {
	s.mu.Lock()

	if isTurn {
		s.turns--
	} else {
		s.gates--
	}

	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
}

func (s *Scheduler) notify(h Hold) {
	s.mu.Lock()
	fn := s.onHold
	s.mu.Unlock()

	if fn != nil {
		fn(h)
	}
}

func (s *Scheduler) read(ctx context.Context) (sysinfo.Memory, bool) {
	mem, err := s.mem(ctx)
	if err != nil || mem.TotalBytes == 0 {
		return sysinfo.Memory{}, false
	}

	return mem, true
}

func free(mem sysinfo.Memory) float64 {
	if mem.TotalBytes == 0 {
		return 1
	}

	return float64(mem.Free()) / float64(mem.TotalBytes)
}

const percent = 100

func holdReason(mem sysinfo.Memory, isTurn bool) string {
	rival := "a turn on the local model"
	if isTurn {
		rival = "a gate run"
	}

	return fmt.Sprintf("held for %s, %.0f%% memory free", rival, free(mem)*percent)
}

// Snapshot reports what is running and what the machine looks like.
func (s *Scheduler) Snapshot(ctx context.Context) Snapshot {
	if s == nil {
		return Snapshot{Phase: PhaseEdit}
	}

	mem, measured := s.read(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	phase := PhaseEdit
	if s.gates > 0 {
		phase = PhaseExecute
	}

	return Snapshot{
		Phase:          phase,
		Memory:         mem,
		MemoryMeasured: measured,
		Headroom:       s.headroom,
		LocalTurns:     s.turns,
		GateRuns:       s.gates,
	}
}
