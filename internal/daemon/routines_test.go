package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/llm/fake"
)

var errUnknownStubRoutine = errors.New("unknown routine")

// stubRoutines is a RoutineSource that records what it was asked to run.
type stubRoutines struct {
	ran chan string
}

func (stubRoutines) List() ([]api.RoutineInfo, error) {
	return []api.RoutineInfo{
		{Name: "gate-format", Triggers: []string{"manual"}, Steps: []string{"format"}, Enabled: true},
	}, nil
}

func (s stubRoutines) Run(_ context.Context, name string) (api.RoutineInfo, error) {
	if name != "gate-format" {
		return api.RoutineInfo{}, fmt.Errorf("%w: %s", errUnknownStubRoutine, name)
	}

	s.ran <- name

	return api.RoutineInfo{Name: name, Enabled: true, Runs: []api.RoutineRun{{Pass: true}}}, nil
}

func TestRoutines_ListAndRunOverTheSocket(t *testing.T) {
	t.Parallel()

	stub := stubRoutines{ran: make(chan string, 1)}
	h := newHarness(t, fake.New("local"), withServerOptions(daemon.WithRoutines(stub)))

	cl := dial(t, h)
	cl.send(api.Command{ID: "hello", Kind: api.CmdHello})
	cl.recvFor("hello")

	cl.send(api.Command{ID: "list", Kind: api.CmdRoutines})

	listed := cl.recvFor("list")
	if listed.Kind != api.RepRoutines || len(listed.Routines) != 1 {
		t.Fatalf("routines reply = %+v, want one routine", listed)
	}
	if listed.Routines[0].Name != "gate-format" {
		t.Errorf("routine name = %q, want gate-format", listed.Routines[0].Name)
	}

	cl.send(api.Command{ID: "run", Kind: api.CmdRunRoutine, Routine: "gate-format"})

	ran := cl.recvFor("run")
	if ran.Kind != api.RepRoutines || len(ran.Routines) != 1 || !ran.Routines[0].Runs[0].Pass {
		t.Fatalf("run reply = %+v, want the routine's completed run", ran)
	}
	if got := <-stub.ran; got != "gate-format" {
		t.Errorf("ran %q, want gate-format", got)
	}

	cl.send(api.Command{ID: "bad", Kind: api.CmdRunRoutine, Routine: "nope"})

	if rep := cl.recvFor("bad"); rep.Kind != api.RepError {
		t.Errorf("unknown routine reply = %+v, want an error", rep)
	}
}

// lifecycleStub records the order a run's boundaries were reported in.
type lifecycleStub struct{ seen chan string }

func (l lifecycleStub) ThreadStarted(context.Context)  { l.seen <- "start" }
func (l lifecycleStub) ThreadFinished(context.Context) { l.seen <- "finish" }

// A routine triggered on a thread rather than on a change needs the daemon
// to say when a run begins and ends, which is the whole of what crosses
// between the two.
func TestRoutines_ThreadLifecycleFiresAroundARun(t *testing.T) {
	t.Parallel()

	stub := lifecycleStub{seen: make(chan string, 4)}
	h := newHarness(t, fake.New("local"), withServerOptions(daemon.WithThreadLifecycle(stub)))

	cl := dial(t, h)
	cl.hello()

	th := cl.newThread([]string{t.TempDir()})
	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})

	cl.send(api.Command{ID: "again", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "more"})

	// start once for the thread, then a finish per run: a thread-start
	// routine sets up what the thread needs, and a second prompt is not a
	// second thread.
	for _, want := range []string{"start", "finish", "finish"} {
		select {
		case got := <-stub.seen:
			if got != want {
				t.Fatalf("lifecycle reported %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q within the deadline", want)
		}
	}
}

func TestRoutines_RefusedWhenTheDaemonHasNone(t *testing.T) {
	t.Parallel()

	h := newHarness(t, fake.New("local"))

	cl := dial(t, h)
	cl.send(api.Command{ID: "hello", Kind: api.CmdHello})
	cl.recvFor("hello")

	cl.send(api.Command{ID: "list", Kind: api.CmdRoutines})

	if rep := cl.recvFor("list"); rep.Kind != api.RepError {
		t.Errorf("reply = %+v, want an error rather than an empty list", rep)
	}
}
