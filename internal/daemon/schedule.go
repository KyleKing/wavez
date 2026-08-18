package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/sched"
)

// One lane spans laneCells glyphs over the last laneWindow, sized to the
// DESIGN.md mock rather than to the data: a lane is a shape to scan, not a
// chart. The state history a lane is drawn from is bounded by laneSamples.
const (
	laneCells   = 15
	laneWindow  = 5 * time.Minute
	laneSamples = 64
)

// stateSample is one thread's state at a moment, kept so a lane can show
// where a thread spent the recent past instead of only where it is now.
type stateSample struct {
	at    time.Time
	state event.State
}

// schedule reports the fleet as the scheduler sees it.
func (s *Server) schedule(ctx context.Context) api.Schedule {
	snap := s.sched.Snapshot(ctx)

	out := api.Schedule{
		Phase:         string(snap.Phase),
		LocalModel:    s.mgr.localModel(),
		Headroom:      snap.Headroom,
		MemMeasured:   snap.MemoryMeasured,
		MemUsedBytes:  snap.Memory.UsedBytes,
		MemTotalBytes: snap.Memory.TotalBytes,
	}

	leases := s.leases.List()
	for _, l := range leases {
		out.Leases = append(out.Leases, api.Lease{
			Subtree: l.Subtree,
			Holder:  s.threadName(l.Holder),
			State:   string(l.State),
			Waiters: s.threadNames(l.Waiters),
		})
	}

	now := time.Now()
	infos := s.mgr.list()
	for i := range infos {
		out.Lanes = append(out.Lanes, s.lane(infos[i], leases, now))
	}

	return out
}

func (s *Server) lane(info api.ThreadInfo, leases []lease.Lease, now time.Time) api.Lane {
	lane := api.Lane{
		ThreadID: info.ID,
		Thread:   info.Name,
		Step:     info.Step,
		Cells:    s.laneCells(info, now),
	}

	if info.State == event.StateGating {
		lane.Gate = info.Step
	}

	for _, l := range leases {
		for _, w := range l.Waiters {
			if w == info.ID {
				lane.Lock, lane.LockHolder = l.Subtree, s.threadName(l.Holder)
			}
		}
	}

	return lane
}

// laneCells buckets a thread's state history into the lane's cells, oldest
// first. A cell before the thread's first sample reads as idle, which is what
// it was.
func (s *Server) laneCells(info api.ThreadInfo, now time.Time) []event.State {
	mt, ok := s.mgr.get(info.ID)
	if !ok {
		return nil
	}

	mt.sync()

	mt.mu.Lock()
	samples := append([]stateSample(nil), mt.samples...)
	mt.mu.Unlock()

	cells := make([]event.State, laneCells)
	step := laneWindow / laneCells
	start := now.Add(-laneWindow)

	for i := range cells {
		at := start.Add(time.Duration(i+1) * step)
		cells[i] = event.StateIdle

		for _, sm := range samples {
			if sm.at.After(at) {
				break
			}

			cells[i] = sm.state
		}
	}

	return cells
}

func (s *Server) threadName(id string) string {
	if id == "" {
		return ""
	}

	mt, ok := s.mgr.get(id)
	if !ok {
		return id
	}

	mt.mu.Lock()
	defer mt.mu.Unlock()

	return mt.name
}

func (s *Server) threadNames(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}

	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.threadName(id))
	}

	return out
}

// noteLeaseWait puts a thread's lock wait where every screen already looks
// for what a thread is doing: its own event log, and so its step column.
func (s *Server) noteLeaseWait(w lease.Wait) {
	mt, ok := s.mgr.get(w.Holder)
	if !ok {
		return
	}

	if !w.Waiting {
		s.setStep(mt, event.StateWorking, "")

		return
	}

	s.setStep(mt, event.StateBlocked, fmt.Sprintf("waiting lock %s ← %s", w.Subtree, s.threadName(w.Blocker)))
}

// noteHold says why a thread admitted nowhere is also doing nothing, which
// is otherwise indistinguishable from a thread that is merely slow.
func (s *Server) noteHold(h sched.Hold) {
	mt, ok := s.mgr.get(h.Holder)
	if !ok {
		return
	}

	if !h.Held {
		s.setStep(mt, event.StateWorking, "")

		return
	}

	s.setStep(mt, event.StateBlocked, h.Reason)
}

// setStep records a lifecycle transition on the thread's own log, which is
// what the manager's watcher turns into the step column.
func (s *Server) setStep(mt *managedThread, state event.State, text string) {
	ev := event.Event{Kind: event.KindState, State: state, Text: text}
	if _, err := mt.th.Log().Append(ev); err != nil {
		return
	}

	s.wakePending()
}
