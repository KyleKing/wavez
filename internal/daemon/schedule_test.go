package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/sched"
	"github.com/kyleking/wavez/internal/sysinfo"
	"github.com/kyleking/wavez/internal/tool"
)

// leasedWrite is a write tool a test drives by hand: it takes the lease for
// the path the test assigned its thread, reports that it holds it, and then
// waits to be told to finish, so the test decides how long each write lasts.
type leasedWrite struct {
	leases  *lease.Manager
	paths   map[string]string
	finish  map[string]chan struct{}
	holding chan string
	echoTool
	mu sync.Mutex
}

func (w *leasedWrite) Run(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	holder, _ := lease.HolderFrom(ctx)

	w.mu.Lock()
	path, finish := w.paths[holder], w.finish[holder]
	w.mu.Unlock()

	release, err := w.leases.Acquire(ctx, path)
	if err != nil {
		return tool.Fail(tool.CauseIO, "%v", err), nil
	}
	defer release()

	w.holding <- holder

	select {
	case <-finish:
		return tool.Result{Content: "wrote " + path}, nil
	case <-ctx.Done():
		return tool.Result{}, fmt.Errorf("leased write: %w", ctx.Err())
	}
}

func (w *leasedWrite) assign(threadID, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.paths[threadID] = path
	w.finish[threadID] = make(chan struct{})
}

func (w *leasedWrite) done(threadID string) {
	w.mu.Lock()
	ch := w.finish[threadID]
	w.mu.Unlock()
	close(ch)
}

func writeTurn() fake.Turn {
	return fake.Turn{
		ToolCalls:  []llm.ToolCall{{ID: "1", Name: "leased", Input: []byte(`{}`)}},
		StopReason: llm.StopToolUse,
	}
}

func endTurn() fake.Turn {
	return fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn}
}

// The M2 done-condition: three threads across two directories run at once,
// the two writing the same subtree serialize on its lease, the third never
// waits, and the schedule reply shows all of it.
func TestSchedule_ThreeThreadsTwoDirectoriesSerializeOnTheLease(t *testing.T) {
	t.Parallel()

	repoA, repoB := t.TempDir(), t.TempDir()
	leases := lease.New(repoA)
	tl := &leasedWrite{
		echoTool: echoTool{name: "leased"},
		leases:   leases,
		paths:    map[string]string{},
		finish:   map[string]chan struct{}{},
		holding:  make(chan string, 3),
	}

	local := fake.New("local", writeTurn(), writeTurn(), writeTurn(), endTurn(), endTurn(), endTurn())
	h := newHarness(t, local, withServerOptions(daemon.WithLeases(leases)), withTool(tl))

	cl := dial(t, h)
	cl.hello()

	alpha := cl.newThread([]string{repoA})
	beta := cl.newThread([]string{repoA})
	gamma := cl.newThread([]string{repoB})

	tl.assign(alpha.ID, filepath.Join(repoA, "internal", "vcs", "a.go"))
	tl.assign(beta.ID, filepath.Join(repoA, "internal", "vcs", "b.go"))
	tl.assign(gamma.ID, filepath.Join(repoB, "internal", "api", "c.go"))

	sendPrompt(t, cl, alpha.ID)
	if got := <-tl.holding; got != alpha.ID {
		t.Fatalf("first holder = %s, want alpha %s", got, alpha.ID)
	}

	sendPrompt(t, cl, beta.ID)
	// The lock on the lane and the words in its step arrive on two paths (the
	// lease list and the thread's own log), so wait for both before judging.
	wantStep := "waiting lock internal/vcs ← " + alpha.Name
	schedule := waitForSchedule(t, cl, func(s api.Schedule) bool {
		l := laneFor(s, beta.ID)

		return l.Lock != "" && l.Step == wantStep
	})
	betaLane := laneFor(schedule, beta.ID)
	if betaLane.Lock != filepath.Join("internal", "vcs") || betaLane.LockHolder != alpha.Name {
		t.Fatalf("beta lane = %+v, want a wait on internal/vcs held by %s", betaLane, alpha.Name)
	}

	sendPrompt(t, cl, gamma.ID)
	if got := <-tl.holding; got != gamma.ID {
		t.Fatalf("second holder = %s, want gamma %s (it must not wait behind alpha)", got, gamma.ID)
	}

	schedule = waitForSchedule(t, cl, func(s api.Schedule) bool { return len(s.Leases) == 2 })
	assertLease(t, schedule, filepath.Join("internal", "vcs"), alpha.Name, "active", beta.Name)
	assertLease(t, schedule, filepath.Join(repoB, "internal", "api"), gamma.Name, "active")

	tl.done(gamma.ID)
	tl.done(alpha.ID)

	if got := <-tl.holding; got != beta.ID {
		t.Fatalf("third holder = %s, want beta %s", got, beta.ID)
	}
	tl.done(beta.ID)

	schedule = waitForSchedule(t, cl, func(s api.Schedule) bool {
		l := leaseFor(s, filepath.Join("internal", "vcs"))

		return l.State == "committed" && len(l.Waiters) == 0
	})
	if got := leaseFor(schedule, filepath.Join("internal", "vcs")).Holder; got != beta.Name {
		t.Fatalf("final holder = %s, want beta %s", got, beta.Name)
	}
	if len(schedule.Lanes) != 3 || schedule.Phase != "edit" {
		t.Fatalf("schedule = %d lanes in phase %q, want 3 lanes in edit", len(schedule.Lanes), schedule.Phase)
	}
}

func sendPrompt(t *testing.T, cl *client, threadID string) {
	t.Helper()

	cl.send(api.Command{ID: "send-" + threadID, Kind: api.CmdSend, ThreadID: threadID, Prompt: "go"})
	if rep := cl.recvFor("send-" + threadID); rep.Kind != api.RepThread {
		t.Fatalf("send %s: %+v", threadID, rep)
	}
}

// waitForSchedule polls the schedule until pred holds, since a lease wait is
// reached on the thread's own goroutine and has no push equivalent.
func waitForSchedule(t *testing.T, cl *client, pred func(api.Schedule) bool) api.Schedule {
	t.Helper()

	for i := 0; ; i++ {
		id := "sched-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
		cl.send(api.Command{ID: id, Kind: api.CmdSchedule})

		rep := cl.recvFor(id)
		if rep.Kind != api.RepSchedule || rep.Schedule == nil {
			t.Fatalf("schedule: %+v", rep)
		}
		if pred(*rep.Schedule) {
			return *rep.Schedule
		}
	}
}

func laneFor(s api.Schedule, threadID string) api.Lane {
	for _, l := range s.Lanes {
		if l.ThreadID == threadID {
			return l
		}
	}

	return api.Lane{}
}

func leaseFor(s api.Schedule, subtree string) api.Lease {
	for _, l := range s.Leases {
		if l.Subtree == subtree {
			return l
		}
	}

	return api.Lease{}
}

func assertLease(t *testing.T, s api.Schedule, subtree, holder, state string, waiters ...string) {
	t.Helper()

	l := leaseFor(s, subtree)
	if l.Holder != holder || l.State != state || len(l.Waiters) != len(waiters) {
		t.Fatalf("lease %s = %+v, want holder %s state %s waiters %v", subtree, l, holder, state, waiters)
	}
	for i, w := range waiters {
		if l.Waiters[i] != w {
			t.Fatalf("lease %s waiters = %v, want %v", subtree, l.Waiters, waiters)
		}
	}
}

// A thread held back by admission says why, since a thread that is neither
// working nor waiting on a lock otherwise reads as merely slow.
func TestSchedule_HeldTurnSaysWhyInItsStep(t *testing.T) {
	t.Parallel()

	const total = 16 << 30

	tight := func(context.Context) (sysinfo.Memory, error) {
		return sysinfo.Memory{TotalBytes: total, UsedBytes: total - (2 << 30)}, nil
	}
	scheduler := sched.New(sched.WithMemory(tight))
	releaseGate, err := scheduler.AdmitGate(t.Context())
	if err != nil {
		t.Fatalf("AdmitGate: %v", err)
	}

	local := fake.New("local", endTurn())
	h := newHarness(t, local, withServerOptions(daemon.WithScheduler(scheduler)))

	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)
	sendPrompt(t, cl, th.ID)

	s := waitForSchedule(t, cl, func(s api.Schedule) bool {
		return strings.HasPrefix(laneFor(s, th.ID).Step, "held for a gate run")
	})
	if s.Phase != "execute" {
		t.Fatalf("phase = %q while a gate run holds admission, want execute", s.Phase)
	}

	releaseGate()

	waitForSchedule(t, cl, func(s api.Schedule) bool { return laneFor(s, th.ID).Step == "done" })
}

// The M2 park/unpark condition: a thread blocked on a permission prompt
// gives back its turn admission rather than squatting on it, so a gate run
// that needs the machine can go ahead while the thread waits on a human.
// When the answer arrives, the thread re-admits, blocking behind that same
// gate run if it still holds the machine, and shows the existing "waiting
// to resume" step rather than a lock-wait glyph while it does.
func TestSchedule_ParkedThreadFreesAdmissionAndReadmitsOnAnswer(t *testing.T) {
	t.Parallel()

	const total = 16 << 30

	tight := func(context.Context) (sysinfo.Memory, error) {
		return sysinfo.Memory{TotalBytes: total, UsedBytes: total - (2 << 30)}, nil
	}
	scheduler := sched.New(sched.WithMemory(tight))

	local := fake.New("local",
		fake.Turn{
			ToolCalls:  []llm.ToolCall{{ID: "1", Name: "gated", Input: []byte(`{}`)}},
			StopReason: llm.StopToolUse,
		},
		endTurn(),
	)
	h := newHarness(t, local,
		withServerOptions(daemon.WithScheduler(scheduler)),
		withTool(gatedTool{echoTool: echoTool{name: "gated"}, key: "gated-key"}))

	watcher := dial(t, h)
	watcher.hello()
	th := watcher.newThread(nil)
	watcher.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	watcher.recvFor("sub")

	sendPrompt(t, watcher, th.ID)

	pending := waitForEvent(t, watcher, func(rep api.Reply) bool {
		return rep.Kind == api.RepPending && len(rep.Pending) == 1
	})
	promptID := pending.Pending[0].ID

	// The thread parked: its own state is needs_input, distinct from a lock
	// wait, and its admission is free for a gate run to take.
	waitForState(t, watcher, th.ID, event.StateNeedsIn)

	releaseGate := admitGateOrTimeout(t, scheduler)

	watcher.send(api.Command{ID: "answer", Kind: api.CmdAnswer, PromptID: promptID, Decision: permission.Allow})
	watcher.recvFor("answer")

	// The gate run still holds the machine, so the thread's re-admission
	// blocks and says so with the same words a plain held turn uses.
	waitForSchedule(t, watcher, func(s api.Schedule) bool {
		return strings.HasPrefix(laneFor(s, th.ID).Step, "held for a gate run")
	})

	releaseGate()

	waitForSchedule(t, watcher, func(s api.Schedule) bool { return laneFor(s, th.ID).Step == "done" })
}

// admitGateOrTimeout proves a rival admission is actually free by racing it
// against a deadline: a park bug that never releases the turn's slot would
// otherwise hang this call rather than fail it.
func admitGateOrTimeout(t *testing.T, scheduler *sched.Scheduler) func() {
	t.Helper()

	type result struct {
		release func()
		err     error
	}

	done := make(chan result, 1)
	go func() {
		release, err := scheduler.AdmitGate(t.Context())
		done <- result{release, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("AdmitGate: %v", r.err)
		}

		return r.release
	case <-time.After(2 * time.Second):
		t.Fatal("AdmitGate did not admit while the parked thread should hold no admission")

		return nil
	}
}

// waitForState polls until threadID reports want, since a park is reached
// on the thread's own goroutine and has no push equivalent.
func waitForState(t *testing.T, cl *client, threadID string, want event.State) {
	t.Helper()

	for i := 0; ; i++ {
		id := "list-" + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
		cl.send(api.Command{ID: id, Kind: api.CmdList})

		rep := cl.recvFor(id)
		if rep.Kind != api.RepThreads {
			t.Fatalf("list: %+v", rep)
		}
		for i := range rep.Threads {
			if rep.Threads[i].ID == threadID && rep.Threads[i].State == want {
				return
			}
		}
	}
}
