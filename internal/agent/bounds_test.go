package agent_test

import (
	"context"
	"encoding/json"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// fakeClock is gate.Clock driven entirely by Advance, so a test never sleeps
// to exercise the deadline bound.
type fakeClock struct {
	now time.Time
	mu  sync.Mutex
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// NewTimer is unused by the agent package's deadline check, which polls
// Now() rather than waiting on a channel, but is required to satisfy
// gate.Clock.
//
//nolint:ireturn // implements gate.Clock's Timer-returning contract
func (*fakeClock) NewTimer(time.Duration) gate.Timer {
	return &fakeTimer{}
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeTimer struct{}

func (fakeTimer) C() <-chan time.Time      { return make(chan time.Time) }
func (fakeTimer) Stop() bool               { return true }
func (fakeTimer) Reset(time.Duration) bool { return true }

// advancingProvider wraps a fake.Provider and moves a fakeClock forward by a
// fixed step on every Stream call, modeling wall time passing during a
// turn's own decode without a real sleep.
type advancingProvider struct {
	inner   *fake.Provider
	clock   *fakeClock
	advance time.Duration
}

func (p *advancingProvider) Name() string { return p.inner.Name() }

func (p *advancingProvider) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	p.clock.Advance(p.advance)

	return p.inner.Stream(ctx, req)
}

type erroringTool struct {
	echoTool
}

func (erroringTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Errorf("boom"), nil
}

func TestRun_DeadlineBoundTrips(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: json.RawMessage(`{"a":1}`)}
	inner := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"turn two"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	clock := newFakeClock(time.Unix(0, 0))
	local := &advancingProvider{inner: inner, clock: clock, advance: 5 * time.Second}

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(local, hosted, reg, permission.AllowAll(),
		agent.WithClock(clock), agent.WithMaxWallClock(time.Second))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopDeadline {
		t.Fatalf("Stop = %q, want deadline", out.Stop)
	}
	if out.Elapsed < 5*time.Second {
		t.Errorf("Elapsed = %s, want at least 5s", out.Elapsed)
	}
	if got := len(inner.Requests()); got != 1 {
		t.Fatalf("local Requests len = %d, want 1: the deadline must trip before a second turn runs", got)
	}
}

// The deadline is an absolute point computed once at Run entry: two turns
// that each individually stay under it can still exceed it in sum, and the
// bound must catch that at the next turn boundary.
func TestRun_DeadlineIsAbsoluteAcrossTurns(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: json.RawMessage(`{"a":1}`)}
	inner := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	clock := newFakeClock(time.Unix(0, 0))
	local := &advancingProvider{inner: inner, clock: clock, advance: 3 * time.Second}

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(local, hosted, reg, permission.AllowAll(),
		agent.WithClock(clock), agent.WithMaxWallClock(5*time.Second))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopDeadline {
		t.Fatalf("Stop = %q, want deadline", out.Stop)
	}
	if got := len(inner.Requests()); got != 2 {
		t.Fatalf("local Requests len = %d, want 2: each turn alone (3s) stayed under the 5s deadline", got)
	}
}

func TestRun_CostCeilingTripsOnlyOnHostedSpend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantStop agent.Stop
		route    router.Input
	}{
		{
			name:     "local usage never trips the ceiling",
			route:    router.Input{Override: router.ChoiceLocal},
			wantStop: agent.StopComplete,
		},
		{
			name:     "hosted usage over the ceiling trips it",
			route:    router.Input{Override: router.ChoiceHosted},
			wantStop: agent.StopCostCeiling,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			usage := &llm.Usage{InputTokens: 10_000_000, OutputTokens: 10_000_000}
			local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn, Usage: usage})
			hosted := fake.New("hosted", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn, Usage: usage})

			th := newThread(t)
			reg := tool.NewRegistry(echoTool{name: "echo"})
			loop := agent.New(local, hosted, reg, permission.AllowAll(),
				agent.WithHostedModel("qwen/qwen3-coder-30b-a3b-instruct"),
				agent.WithMaxHostedSpendUSD(0.01))

			out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", tt.route)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if out.Stop != tt.wantStop {
				t.Fatalf("Stop = %q, want %q", out.Stop, tt.wantStop)
			}
			if tt.wantStop == agent.StopCostCeiling && out.HostedSpendUSD <= 0 {
				t.Errorf("HostedSpendUSD = %v, want > 0", out.HostedSpendUSD)
			}
			if tt.wantStop == agent.StopComplete && out.HostedSpendUSD != 0 {
				t.Errorf("HostedSpendUSD = %v, want 0 for a local-only run", out.HostedSpendUSD)
			}
		})
	}
}

func TestRun_StagnationBoundTrips(t *testing.T) {
	t.Parallel()

	failCall := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "fail", Input: json.RawMessage(`{"id":"` + id + `"}`)}
	}

	tests := []struct {
		name         string
		wantStop     agent.Stop
		script       []fake.Turn
		wantStagnant int
	}{
		{
			name: "three consecutive errors trip stagnation",
			script: []fake.Turn{
				{ToolCalls: []llm.ToolCall{failCall("1")}, StopReason: llm.StopToolUse},
				{ToolCalls: []llm.ToolCall{failCall("2")}, StopReason: llm.StopToolUse},
				{ToolCalls: []llm.ToolCall{failCall("3")}, StopReason: llm.StopToolUse},
			},
			wantStop:     agent.StopStagnant,
			wantStagnant: 3,
		},
		{
			name: "two errors then a success does not trip",
			script: []fake.Turn{
				{ToolCalls: []llm.ToolCall{failCall("1")}, StopReason: llm.StopToolUse},
				{ToolCalls: []llm.ToolCall{failCall("2")}, StopReason: llm.StopToolUse},
				{
					ToolCalls:  []llm.ToolCall{{ID: "3", Name: "echo", Input: json.RawMessage(`{"a":1}`)}},
					StopReason: llm.StopToolUse,
				},
				{Text: []string{"done"}, StopReason: llm.StopEndTurn},
			},
			wantStop: agent.StopComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			local := fake.New("local", tt.script...)
			hosted := fake.New("hosted")

			th := newThread(t)
			reg := tool.NewRegistry(erroringTool{echoTool: echoTool{name: "fail"}}, echoTool{name: "echo"})
			loop := agent.New(local, hosted, reg, permission.AllowAll(), agent.WithMaxStagnantErrors(3))

			out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if out.Stop != tt.wantStop {
				t.Fatalf("Stop = %q, want %q", out.Stop, tt.wantStop)
			}
			if tt.wantStagnant > 0 {
				if out.StagnantTool != "fail" {
					t.Errorf("StagnantTool = %q, want fail", out.StagnantTool)
				}
				if out.StagnantCount != tt.wantStagnant {
					t.Errorf("StagnantCount = %d, want %d", out.StagnantCount, tt.wantStagnant)
				}
			}
		})
	}
}

func TestRun_ToolCallFloodGuardTripsPerTurn(t *testing.T) {
	t.Parallel()

	calls := []llm.ToolCall{
		{ID: "1", Name: "echo", Input: json.RawMessage(`{"a":1}`)},
		{ID: "2", Name: "echo", Input: json.RawMessage(`{"a":2}`)},
		{ID: "3", Name: "echo", Input: json.RawMessage(`{"a":3}`)},
	}
	local := fake.New("local", fake.Turn{ToolCalls: calls, StopReason: llm.StopToolUse})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(local, hosted, reg, permission.AllowAll(), agent.WithMaxToolCallsPerTurn(2))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopToolCallFlood {
		t.Fatalf("Stop = %q, want tool_call_flood", out.Stop)
	}
	if out.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2: the guard trips before the third call runs", out.ToolCalls)
	}
}

func TestRun_BoundStillVerifiesAbandonedChangesAndLogsCheckpoint(t *testing.T) {
	t.Parallel()

	calls := []llm.ToolCall{
		{ID: "1", Name: "changer", Input: json.RawMessage(`{"a":1}`)},
		{ID: "2", Name: "changer", Input: json.RawMessage(`{"a":2}`)},
	}
	local := fake.New("local", fake.Turn{ToolCalls: calls, StopReason: llm.StopToolUse})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(changeTool{name: "changer", changes: []tool.Change{{Path: "a.go", Added: 1}}})
	v := &stubVerifier{}
	cp := &stubCheckpointer{captured: "op-bound"}
	loop := agent.New(local, hosted, reg, permission.AllowAll(),
		agent.WithMaxToolCallsPerTurn(1), agent.WithVerifier(v), agent.WithCheckpointer(cp, "/repo"))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopToolCallFlood {
		t.Fatalf("Stop = %q, want tool_call_flood", out.Stop)
	}
	if len(v.calls) == 0 {
		t.Fatal("a bounded run left changed files unverified")
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var loggedGate, loggedCheckpoint bool

	for _, ev := range events {
		if ev.Kind == event.KindGate {
			if abandoned, ok := ev.Detail["abandoned"].(bool); ok && abandoned {
				loggedGate = true
			}
		}
		if ev.Kind == event.KindError {
			if got, ok := ev.Detail["checkpoint"].(string); ok && got == "op-bound" {
				loggedCheckpoint = true
			}
		}
	}

	if !loggedGate {
		t.Error("the abandoned change set's gate result was never logged")
	}
	if !loggedCheckpoint {
		t.Error("the bound event never carried the checkpoint id")
	}
}

func TestRun_AllBoundsDisabledBehaviorUnchanged(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: json.RawMessage(`{"a":1}`)}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(local, hosted, reg, permission.AllowAll(),
		agent.WithMaxWallClock(0), agent.WithMaxHostedSpendUSD(0), agent.WithMaxStagnantErrors(0))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete {
		t.Fatalf("Stop = %q, want complete", out.Stop)
	}
	if out.Turns != 2 || out.ToolCalls != 1 {
		t.Errorf("Turns=%d ToolCalls=%d, want 2 and 1", out.Turns, out.ToolCalls)
	}
}
