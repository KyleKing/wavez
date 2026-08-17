package daemon_test

import (
	"context"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// routedHarness is newHarness with both providers scripted, so a test can
// tell which tier actually served a turn.
type routedHarness struct {
	*testHarness
	local  *fake.Provider
	hosted *fake.Provider
}

func newRoutedHarness(t *testing.T) *routedHarness {
	t.Helper()

	answer := fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn}
	local := fake.New("local", answer)
	hosted := fake.New("hosted", answer)
	broker := daemon.NewBroker()
	loop := agent.New(local, hosted, tool.NewRegistry(echoTool{name: "echo"}), broker.Gate(),
		agent.WithLocalModel("qwen3:8b"))

	sockPath := shortSockPath(t)
	srv, err := daemon.New(sockPath,
		daemon.WithLoop(loop),
		daemon.WithBroker(broker),
		daemon.WithLogDir(t.TempDir()),
		daemon.WithPrefix(agent.Prefix{System: "test"}),
		daemon.WithShutdownGrace(2*time.Second),
	)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serveErr; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	waitForSocket(t, sockPath)

	return &routedHarness{
		testHarness: &testHarness{server: srv, broker: broker, sockPath: sockPath},
		local:       local,
		hosted:      hosted,
	}
}

// A pin has to reach the agent loop's routing input, not just the thread
// record a client reads back.
func TestRoute_PinDecidesWhichTierServesTheNextTurn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		override router.Choice
		wantTier string
	}{
		{name: "pinned hosted", override: router.ChoiceHosted, wantTier: "hosted"},
		{name: "pinned local", override: router.ChoiceLocal, wantTier: "local"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newRoutedHarness(t)
			cl := dial(t, h.testHarness)
			cl.hello()
			th := cl.newThread([]string{t.TempDir()})

			cl.send(api.Command{ID: "route", Kind: api.CmdRoute, ThreadID: th.ID, Override: tc.override})

			rep := cl.recvFor("route")
			if rep.Thread == nil || rep.Thread.Override != tc.override {
				t.Fatalf("route reply = %+v, want Override %q", rep, tc.override)
			}

			cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
			cl.recvFor("sub")
			cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "hi"})

			waitForEvent(t, cl, func(rep api.Reply) bool {
				return rep.Kind == api.RepEvent && rep.Event != nil &&
					rep.Event.Kind == event.KindState && rep.Event.State == event.StateDone
			})

			served := "local"
			if len(h.hosted.Requests()) > 0 {
				served = "hosted"
			}
			if served != tc.wantTier || len(h.local.Requests())+len(h.hosted.Requests()) != 1 {
				t.Fatalf("served %s (local=%d hosted=%d), want %s",
					served, len(h.local.Requests()), len(h.hosted.Requests()), tc.wantTier)
			}
		})
	}
}

// Clearing is the way off a broken tier, so an empty override has to be
// accepted rather than rejected as an unknown one.
func TestRoute_OverrideClearsAndRefusesAnUnknownTier(t *testing.T) {
	t.Parallel()

	h := newRoutedHarness(t)
	cl := dial(t, h.testHarness)
	cl.hello()
	th := cl.newThread([]string{t.TempDir()})

	cl.send(api.Command{ID: "pin", Kind: api.CmdRoute, ThreadID: th.ID, Override: router.ChoiceHosted})
	cl.recvFor("pin")

	cl.send(api.Command{ID: "bad", Kind: api.CmdRoute, ThreadID: th.ID, Override: "gpu"})

	if rep := cl.recvFor("bad"); rep.Kind != api.RepError {
		t.Fatalf("unknown tier reply = %+v, want an error", rep)
	}

	cl.send(api.Command{ID: "clear", Kind: api.CmdRoute, ThreadID: th.ID})

	rep := cl.recvFor("clear")
	if rep.Thread == nil || rep.Thread.Override != "" {
		t.Fatalf("clear reply = %+v, want an empty Override", rep)
	}
}
