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

// TestRun_LocalIsNotRetriedPastOneFailureUnderAnOverride pins the rule the
// router normally enforces through PriorFailures. An explicit tier override
// wins over that check, so before this a run pinned local against a failing
// provider retried until the turn bound: 200 turns in six seconds.
func TestRun_LocalIsNotRetriedPastOneFailureUnderAnOverride(t *testing.T) {
	t.Parallel()

	failing := fake.Turn{Err: agent.ErrScriptedFailure, StopReason: llm.StopEndTurn}
	local := fake.New("local", failing, failing, failing, failing)

	loop := agent.New(local, fake.New("hosted"), tool.NewRegistry(echoTool{name: "echo"}),
		permission.AllowAll())

	hint := router.Input{Override: router.ChoiceLocal}

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "go", hint)
	if !errors.Is(err, agent.ErrScriptedFailure) {
		t.Fatalf("Run error = %v, want a provider failure", err)
	}

	if out.Stop != agent.StopFailed {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopFailed)
	}

	if out.Turns > 2 {
		t.Errorf("Turns = %d, want at most 2; local was retried past one failure", out.Turns)
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

	loop := agent.New(hangingProvider{name: "local"}, fake.New("hosted"),
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
