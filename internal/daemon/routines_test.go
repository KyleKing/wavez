package daemon_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/llm/fake"
)

// stubRoutines is a RoutineSource that records what it was asked to run.
type stubRoutines struct {
	ran chan string
}

func (s stubRoutines) List() ([]api.RoutineInfo, error) {
	return []api.RoutineInfo{
		{Name: "gate-format", Triggers: []string{"manual"}, Steps: []string{"format"}, Enabled: true},
	}, nil
}

func (s stubRoutines) Run(_ context.Context, name string) (api.RoutineInfo, error) {
	if name != "gate-format" {
		return api.RoutineInfo{}, errors.New("unknown routine " + name)
	}

	s.ran <- name

	return api.RoutineInfo{Name: name, Enabled: true, Runs: []api.RoutineRun{{Pass: true}}}, nil
}

func TestRoutines_ListAndRunOverTheSocket(t *testing.T) {
	t.Parallel()

	stub := stubRoutines{ran: make(chan string, 1)}
	h := newHarnessWith(t, fake.New("local"), nil, []daemon.Option{daemon.WithRoutines(stub)})

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
