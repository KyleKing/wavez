package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

var (
	errDiskFull   = errors.New("disk full")
	errLocalCrash = errors.New("local runtime crashed")
)

type echoTool struct {
	name string
}

func (e echoTool) Name() string          { return e.name }
func (echoTool) Description() string     { return "echoes its input" }
func (echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (echoTool) Run(_ context.Context, input json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok:" + string(input)}, nil
}

type gatedTool struct {
	echoTool
	key string
}

func (g gatedTool) RequestPermission(json.RawMessage) (permission.Request, bool) {
	return permission.Request{Tool: g.name, Action: "write", Key: g.key}, true
}

type failingTool struct {
	echoTool
}

func (failingTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, errDiskFull
}

func newThread(t *testing.T) *thread.Thread {
	t.Helper()
	th, err := thread.Open(t.TempDir(), "t1", []string{"/repo"})
	if err != nil {
		t.Fatalf("thread.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := th.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return th
}

func basicPrefix() agent.Prefix {
	return agent.Prefix{
		System: "you are wavez",
		Ledger: "0 turns, 0 files changed, 0 gates run",
		Tools:  []llm.ToolSpec{{Name: "echo", Description: "echoes", Schema: json.RawMessage(`{}`)}},
	}
}

func rawJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return b
}

func eventKinds(t *testing.T, th *thread.Thread) []event.Kind {
	t.Helper()
	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	kinds := make([]event.Kind, len(events))
	for i := range events {
		kinds[i] = events[i].Kind
	}

	return kinds
}

func TestRun_TwoTurnConversationWithOneToolCall(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: rawJSON(t, map[string]any{"a": 1})}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete {
		t.Errorf("Stop = %q, want complete", out.Stop)
	}
	if out.Turns != 2 {
		t.Errorf("Turns = %d, want 2", out.Turns)
	}
	if out.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", out.ToolCalls)
	}

	wantSeq := []event.Kind{
		event.KindUser,
		event.KindState, // working
		event.KindAgent, // turn 1 marker: no tool_call chunk events, just the summary
		event.KindTool,  // echo result
		event.KindAgent, // "done" text chunk streamed
		event.KindAgent, // turn 2 marker
		event.KindState, // done
	}
	if got := eventKinds(t, th); !reflect.DeepEqual(got, wantSeq) {
		t.Fatalf("event kinds = %v, want %v", got, wantSeq)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if role := events[2].Role; role != event.RoleNote {
		t.Errorf("turn 1 marker Role = %q, want note: it precedes a tool call", role)
	}
	if role := events[5].Role; role != event.RoleAnswer {
		t.Errorf("turn 2 marker Role = %q, want answer: it ends the run with no tool call", role)
	}

	history := th.History()
	if len(history) != 4 {
		t.Fatalf("history len = %d, want 4 (user, assistant, tool, assistant)", len(history))
	}
	if history[2].Content != "ok:"+string(call.Input) {
		t.Errorf("tool result content = %q", history[2].Content)
	}
}

func TestRun_MalformedToolCallTerminates(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: json.RawMessage(`{not valid`)}
	local := fake.New("local", fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopMalformedTool {
		t.Fatalf("Stop = %q, want malformed_tool_call", out.Stop)
	}
	if out.ToolCalls != 0 {
		t.Errorf("ToolCalls = %d, want 0: a malformed call must not run", out.ToolCalls)
	}
	if th.State() != event.StateFailed {
		t.Errorf("State = %q, want failed", th.State())
	}

	kinds := eventKinds(t, th)
	if kinds[len(kinds)-2] != event.KindError {
		t.Fatalf("second-to-last event kind = %q, want error", kinds[len(kinds)-2])
	}
}

// A repeat is evidence the tier is stuck, so the first one escalates rather
// than killing the thread; only a repeat after escalating is a loop.
func TestRun_RepeatedToolCallEscalatesThenStops(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: rawJSON(t, map[string]any{"a": 1})}
	repeat := llm.ToolCall{ID: "2", Name: call.Name, Input: call.Input}
	again := llm.ToolCall{ID: "3", Name: call.Name, Input: call.Input}

	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{ToolCalls: []llm.ToolCall{repeat}, StopReason: llm.StopToolUse},
	)
	hosted := fake.New("hosted",
		fake.Turn{ToolCalls: []llm.ToolCall{again}, StopReason: llm.StopToolUse},
	)

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopLoopDetected {
		t.Fatalf("Stop = %q, want loop_detected once the hosted tier repeats too", out.Stop)
	}
	if len(hosted.Requests()) == 0 {
		t.Fatal("a repeated call did not escalate to the hosted tier")
	}

	var critique bool
	for _, msg := range th.History() {
		if msg.Role == llm.RoleTool && msg.IsError && strings.Contains(msg.Content, "already made this exact") {
			critique = true
		}
	}
	if !critique {
		t.Fatal("the model was never told why its repeat was rejected")
	}
}

func TestRun_MaxTurnsBoundTrips(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: rawJSON(t, map[string]any{"a": 1})}
	local := fake.New("local", fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithMaxTurns(1))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopMaxTurns {
		t.Fatalf("Stop = %q, want max_turns", out.Stop)
	}
	if out.Turns != 1 {
		t.Errorf("Turns = %d, want 1", out.Turns)
	}
}

func TestRun_MaxToolCallsBoundTrips(t *testing.T) {
	t.Parallel()

	calls := []llm.ToolCall{
		{ID: "1", Name: "echo", Input: rawJSON(t, map[string]any{"a": 1})},
		{ID: "2", Name: "echo", Input: rawJSON(t, map[string]any{"a": 2})},
	}
	local := fake.New("local", fake.Turn{ToolCalls: calls, StopReason: llm.StopToolUse})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithMaxToolCallsPerTurn(1))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopToolCallFlood {
		t.Fatalf("Stop = %q, want tool_call_flood", out.Stop)
	}
	if out.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1: the bound trips before the second call runs", out.ToolCalls)
	}
}

func TestRun_PrefixStableAcrossTurns(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "echo", Input: rawJSON(t, map[string]any{"a": 1})}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	if _, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := local.Requests()
	if len(reqs) != 2 {
		t.Fatalf("Requests len = %d, want 2", len(reqs))
	}
	if reqs[0].System != reqs[1].System {
		t.Errorf("System differs across turns:\n%q\n%q", reqs[0].System, reqs[1].System)
	}
	if !reflect.DeepEqual(reqs[0].Tools, reqs[1].Tools) {
		t.Errorf("Tools differs across turns:\n%+v\n%+v", reqs[0].Tools, reqs[1].Tools)
	}
	if len(reqs[1].Messages) <= len(reqs[0].Messages) {
		t.Errorf("Messages did not grow: turn1=%d turn2=%d", len(reqs[0].Messages), len(reqs[1].Messages))
	}
}

func TestRun_CancellationMidStreamLeavesConsistentState(t *testing.T) {
	t.Parallel()

	turn := fake.Turn{Text: []string{"slow response"}, StopReason: llm.StopEndTurn, Delay: 50 * time.Millisecond}
	local := fake.New("local", turn)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	out, err := loop.Run(ctx, th, basicPrefix(), "do it", router.Input{})
	if err == nil {
		t.Fatal("Run: want error on cancellation")
	}
	if out.Stop != agent.StopCanceled {
		t.Fatalf("Stop = %q, want canceled", out.Stop)
	}

	history := th.History()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1 (only the user prompt, no partial assistant turn)", len(history))
	}

	kinds := eventKinds(t, th)
	last := kinds[len(kinds)-1]
	if last != event.KindState {
		t.Fatalf("last event kind = %q, want state", last)
	}
}

func TestRun_PermissionGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		decision    permission.Decision
		wantContent string
	}{
		{name: "allow runs the tool", decision: permission.Allow, wantContent: "ok:"},
		{
			name: "deny returns an error result without running the tool", decision: permission.Deny,
			wantContent: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			call := llm.ToolCall{ID: "1", Name: "gated", Input: rawJSON(t, map[string]any{"a": 1})}
			local := fake.New("local",
				fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
				fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
			)
			hosted := fake.New("hosted")

			th := newThread(t)
			reg := tool.NewRegistry(gatedTool{echoTool: echoTool{name: "gated"}, key: "gated-key"})
			gate := permission.GateFunc(func(context.Context, permission.Request) (permission.Decision, error) {
				return tt.decision, nil
			})
			loop := agent.New(tiers(local, hosted), reg, gate)

			out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if out.Stop != agent.StopComplete {
				t.Fatalf("Stop = %q, want complete", out.Stop)
			}

			history := th.History()
			if len(history) < 3 || history[2].Content == "" {
				t.Fatalf("history = %+v, want a tool result", history)
			}
			if got := history[2].Content; !strings.HasPrefix(got, tt.wantContent) {
				t.Errorf("tool result = %q, want prefix %q", got, tt.wantContent)
			}
		})
	}
}

func TestRun_ToolExecutionErrorFeedsBackToModel(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "fail", Input: rawJSON(t, map[string]any{"a": 1})}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(failingTool{echoTool: echoTool{name: "fail"}})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete {
		t.Fatalf("Stop = %q, want complete: a tool error is not a bound", out.Stop)
	}

	history := th.History()
	if !history[2].IsError {
		t.Errorf("tool result IsError = false, want true")
	}
}

func TestRun_LocalFailureEscalatesToHostedOnce(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Err: errLocalCrash})
	hosted := fake.New("hosted", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete {
		t.Fatalf("Stop = %q, want complete", out.Stop)
	}
	if len(local.Requests()) != 1 {
		t.Errorf("local Requests len = %d, want 1: local must not be retried past one failure", len(local.Requests()))
	}
	if len(hosted.Requests()) != 1 {
		t.Errorf("hosted Requests len = %d, want 1", len(hosted.Requests()))
	}
}

// tiers wires primary to the tier a turn starts on and to the tier below,
// and escalated to the tier a failure moves up into, so a test can script
// one provider for the ordinary path and one for the escalated path.
func tiers(primary, escalated llm.Provider) router.Tiers[llm.Provider] {
	return router.Tiers[llm.Provider]{Fast: primary, Balanced: primary, Deep: escalated}
}
