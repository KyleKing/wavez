package agent_test

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// TestRun_EachTierIsTriedAtMostOnce pins the rule the router enforces
// through PriorFailures: a failing tier escalates rather than retrying
// itself, and the run stops once the top tier fails. Before this a run
// pinned to a failing provider retried until the turn bound: 200 turns in
// six seconds. The pin is a floor, so pinning the cheapest tier still walks
// all three.
func TestRun_EachTierIsTriedAtMostOnce(t *testing.T) {
	t.Parallel()

	failing := fake.Turn{Err: agent.ErrScriptedFailure, StopReason: llm.StopEndTurn}
	script := []fake.Turn{failing, failing, failing, failing}
	fastP := fake.New("fast", script...)
	balancedP := fake.New("balanced", script...)
	deepP := fake.New("deep", script...)

	loop := agent.New(router.Tiers[llm.Provider]{Fast: fastP, Balanced: balancedP, Deep: deepP},
		tool.NewRegistry(echoTool{name: "echo"}), permission.AllowAll())

	hint := router.Input{Override: router.ChoiceFast}

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "go", hint)
	if !errors.Is(err, agent.ErrScriptedFailure) {
		t.Fatalf("Run error = %v, want a provider failure", err)
	}

	if out.Stop != agent.StopFailed {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopFailed)
	}

	if out.Turns != 3 {
		t.Errorf("Turns = %d, want one per tier", out.Turns)
	}

	for _, p := range []*fake.Provider{fastP, balancedP, deepP} {
		if got := len(p.Requests()); got != 1 {
			t.Errorf("%s was asked %d times, want exactly 1", p.Name(), got)
		}
	}
}

// hangingProvider accepts a request and never sends, which outlives every
// bound: the deadline is checked between turns and before tool calls, and
// neither happens while a stream is open.
type hangingProvider struct{ name string }

func (p hangingProvider) Name() string { return p.name }

func (hangingProvider) Stream(ctx context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		<-ctx.Done()
		yield(llm.Chunk{}, ctx.Err())
	}
}

// TestRun_DeadlineCutsOffAHungStream pins that -max-wall-clock binds a
// provider that stops sending. A run against one stalled nine minutes with
// a 180s bound configured, because nothing bounded the stream itself.
func TestRun_DeadlineCutsOffAHungStream(t *testing.T) {
	t.Parallel()

	loop := agent.New(tiers(hangingProvider{name: "fast"}, fake.New("deep")),
		tool.NewRegistry(echoTool{name: "echo"}), permission.AllowAll(),
		agent.WithMaxWallClock(100*time.Millisecond))

	done := make(chan agent.Outcome, 1)

	go func() {
		out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "go", router.Input{})
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		done <- out
	}()

	select {
	case out := <-done:
		if out.Stop != agent.StopDeadline {
			t.Errorf("Stop = %q, want %q", out.Stop, agent.StopDeadline)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return; the wall-clock bound does not bound a hung stream")
	}
}
