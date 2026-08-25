package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
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
	return tool.Fail(tool.CauseIO, "boom"), nil
}

type refusingTool struct {
	echoTool
}

func (refusingTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Fail(tool.CauseRefused, "not available here; use echo"), nil
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
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
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
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
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
			route:    router.Input{Override: router.ChoiceFast},
			wantStop: agent.StopComplete,
		},
		{
			name:     "hosted usage over the ceiling trips it",
			route:    router.Input{Override: router.ChoiceDeep},
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
			loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
				agent.WithModels(router.Tiers[string]{Deep: "qwen/qwen3-coder-30b-a3b-instruct"}),
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
			// A refusal names the tool to use instead, so counting it
			// stops a run for having been told something. Two lanes
			// reached for a refused shell command and had one attempt
			// left before the bound.
			name: "a refusal between two errors does not trip",
			script: []fake.Turn{
				{ToolCalls: []llm.ToolCall{failCall("1")}, StopReason: llm.StopToolUse},
				{
					ToolCalls:  []llm.ToolCall{{ID: "2", Name: "refuse", Input: json.RawMessage(`{"a":1}`)}},
					StopReason: llm.StopToolUse,
				},
				{ToolCalls: []llm.ToolCall{failCall("3")}, StopReason: llm.StopToolUse},
				{Text: []string{"done"}, StopReason: llm.StopEndTurn},
			},
			wantStop: agent.StopComplete,
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
			reg := tool.NewRegistry(
				erroringTool{echoTool: echoTool{name: "fail"}},
				refusingTool{echoTool: echoTool{name: "refuse"}},
				echoTool{name: "echo"},
			)
			loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithMaxStagnantErrors(3))

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
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithMaxToolCallsPerTurn(2))

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
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
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
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
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

// advancingTool moves a fakeClock forward on every call and counts how many
// times it ran, so a test can see where inside a turn a bound tripped.
type advancingTool struct {
	clock *fakeClock
	echoTool
	runs    atomic.Int64
	advance time.Duration
}

func (t *advancingTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	t.clock.Advance(t.advance)
	t.runs.Add(1)

	return tool.Result{Content: "ok"}, nil
}

// A turn may issue up to MaxToolCallsPerTurn calls, so a deadline enforced
// only between turns does not bound the edits inside one.
func TestRun_DeadlineTripsWithinATurn(t *testing.T) {
	t.Parallel()

	calls := make([]llm.ToolCall, 0, 4)
	for i := range 4 {
		calls = append(calls, llm.ToolCall{
			ID:    strconv.Itoa(i),
			Name:  "echo",
			Input: json.RawMessage(fmt.Sprintf(`{"a":%d}`, i)),
		})
	}

	inner := fake.New("local",
		fake.Turn{ToolCalls: calls, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	clock := newFakeClock(time.Unix(0, 0))
	local := &advancingProvider{inner: inner, clock: clock, advance: time.Second}
	echo := &advancingTool{echoTool: echoTool{name: "echo"}, clock: clock, advance: 2 * time.Second}

	th := newThread(t)
	loop := agent.New(tiers(local, hosted), tool.NewRegistry(echo), permission.AllowAll(),
		agent.WithClock(clock), agent.WithMaxWallClock(5*time.Second))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopDeadline {
		t.Fatalf("Stop = %q, want deadline", out.Stop)
	}
	if got := echo.runs.Load(); got != 2 {
		t.Errorf("tool runs = %d, want 2: the deadline must trip mid-turn, not after all 4 calls", got)
	}
}

// A run stopped at the ceiling is meant to be picked back up: the ceiling is
// a runaway guard on one unattended run, not a budget for the work. Before
// the history sidecar, resuming handed the model an empty transcript, so
// everything the stopped run had learned was spent again.
func TestRun_CostCeilingLeavesTheThreadResumable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	deep := router.Input{Override: router.ChoiceDeep}
	models := router.Tiers[string]{Deep: "qwen/qwen3-coder-30b-a3b-instruct"}
	usage := &llm.Usage{InputTokens: 10_000_000, OutputTokens: 10_000_000}

	first := fake.New("hosted", fake.Turn{Text: []string{"read the parser"}, StopReason: llm.StopEndTurn, Usage: usage})
	th, err := thread.Open(dir, "t1", []string{"/repo"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	loop := agent.New(tiers(fake.New("local"), first), tool.NewRegistry(echoTool{name: "echo"}),
		permission.AllowAll(), agent.WithModels(models), agent.WithMaxHostedSpendUSD(0.01))

	stopped, err := loop.Run(context.Background(), th, basicPrefix(), "fix the parser", deep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stopped.Stop != agent.StopCostCeiling {
		t.Fatalf("Stop = %q, want cost_ceiling", stopped.Stop)
	}
	if !strings.Contains(stopped.Reason, "keeps its transcript") {
		t.Errorf("Reason = %q, want it to say the thread can be continued", stopped.Reason)
	}
	if err := th.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := fake.New("hosted", fake.Turn{Text: []string{"fixed"}, StopReason: llm.StopEndTurn})
	resumed, err := thread.Open(dir, "t1", []string{"/repo"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer resumed.Close() //nolint:errcheck // the assertions below are what the test reports

	loop = agent.New(tiers(fake.New("local"), second), tool.NewRegistry(echoTool{name: "echo"}),
		permission.AllowAll(), agent.WithModels(models), agent.WithMaxHostedSpendUSD(100))

	out, err := loop.Run(context.Background(), resumed, basicPrefix(), "keep going", deep)
	if err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if out.Stop != agent.StopComplete {
		t.Fatalf("Stop = %q, want complete", out.Stop)
	}

	sent := second.Requests()
	if len(sent) != 1 {
		t.Fatalf("Requests len = %d, want 1", len(sent))
	}
	if got := len(sent[0].Messages); got != 3 {
		t.Fatalf("resumed request carried %d messages, want the two from the stopped run plus the new prompt", got)
	}
	if sent[0].Messages[1].Content != "read the parser" {
		t.Errorf("second message = %q, want the stopped run's own turn", sent[0].Messages[1].Content)
	}
	if out.ThreadSpendUSD <= out.HostedSpendUSD {
		t.Errorf("ThreadSpendUSD = %v, want it to carry the stopped run's $%v too",
			out.ThreadSpendUSD, stopped.HostedSpendUSD)
	}
}
