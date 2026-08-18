package daemon_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/sched"
	"github.com/kyleking/wavez/internal/sysinfo"
	"github.com/kyleking/wavez/internal/tool"
)

// leasedWrite is a write tool a test drives by hand: it takes the lease for
// the path the test assigned its thread, reports that it holds it, and then
// waits to be told to finish, so the test decides how long each write lasts.
type leasedWrite struct {
	echoTool
	leases  *lease.Manager
	paths   map[string]string
	holding chan string
	finish  map[string]chan struct{}
	mu      sync.Mutex
}

func (w *leasedWrite) Run(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	holder, _ := lease.HolderFrom(ctx)

	w.mu.Lock()
	path, finish := w.paths[holder], w.finish[holder]
	w.mu.Unlock()

	release, err := w.leases.Acquire(ctx, path)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	defer release()

	w.holding <- holder
	<-finish

	return tool.Result{Content: "wrote " + path}, nil
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
	h := newHarnessWith(t, local, nil, []daemon.Option{daemon.WithLeases(leases)}, tl)

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
	sched := waitForSchedule(t, cl, func(s api.Schedule) bool {
		return laneFor(s, beta.ID).Lock != ""
	})
	betaLane := laneFor(sched, beta.ID)
	if betaLane.Lock != filepath.Join("internal", "vcs") || betaLane.LockHolder != alpha.Name {
		t.Fatalf("beta lane = %+v, want a wait on internal/vcs held by %s", betaLane, alpha.Name)
	}
	if want := "waiting lock internal/vcs ← " + alpha.Name; betaLane.Step != want {
		t.Fatalf("beta step = %q, want %q", betaLane.Step, want)
	}

	sendPrompt(t, cl, gamma.ID)
	if got := <-tl.holding; got != gamma.ID {
		t.Fatalf("second holder = %s, want gamma %s (it must not wait behind alpha)", got, gamma.ID)
	}

	sched = waitForSchedule(t, cl, func(s api.Schedule) bool { return len(s.Leases) == 2 })
	assertLease(t, sched, filepath.Join("internal", "vcs"), alpha.Name, "active", beta.Name)
	assertLease(t, sched, filepath.Join(repoB, "internal", "api"), gamma.Name, "active")

	tl.done(gamma.ID)
	tl.done(alpha.ID)

	if got := <-tl.holding; got != beta.ID {
		t.Fatalf("third holder = %s, want beta %s", got, beta.ID)
	}
	tl.done(beta.ID)

	sched = waitForSchedule(t, cl, func(s api.Schedule) bool {
		l := leaseFor(s, filepath.Join("internal", "vcs"))

		return l.State == "committed" && len(l.Waiters) == 0
	})
	if got := leaseFor(sched, filepath.Join("internal", "vcs")).Holder; got != beta.Name {
		t.Fatalf("final holder = %s, want beta %s", got, beta.Name)
	}
	if len(sched.Lanes) != 3 || sched.Phase != "edit" {
		t.Fatalf("schedule = %d lanes in phase %q, want 3 lanes in edit", len(sched.Lanes), sched.Phase)
	}
}

func sendPrompt(t *testing.T, cl *client, threadID string) {
	t.Helper()

	cl.send(api.Command{ID: "send-" + threadID, Kind: api.CmdSend, ThreadID: threadID, Prompt: "write"})
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
	h := newHarnessWith(t, local, nil, []daemon.Option{daemon.WithScheduler(scheduler)})

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
