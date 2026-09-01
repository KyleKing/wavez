package thread_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

func open(t *testing.T, opts ...thread.Option) *thread.Thread {
	t.Helper()
	th, err := thread.Open(t.TempDir(), "t1", []string{"/repo"}, opts...)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := th.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return th
}

func TestOpenSetsIdentity(t *testing.T) {
	t.Parallel()

	th := open(t, thread.WithModel("qwen3:8b"), thread.WithParent("parent-1"))

	if th.ID() != "t1" {
		t.Errorf("ID = %q, want t1", th.ID())
	}
	if got := th.Dirs(); len(got) != 1 || got[0] != "/repo" {
		t.Errorf("Dirs = %v, want [/repo]", got)
	}
	if th.Model() != "qwen3:8b" {
		t.Errorf("Model = %q, want qwen3:8b", th.Model())
	}
	if th.Parent() != "parent-1" {
		t.Errorf("Parent = %q, want parent-1", th.Parent())
	}
	if th.State() != event.StateIdle {
		t.Errorf("State = %q, want idle", th.State())
	}
}

func TestHistoryIsAppendOnlyAndCopied(t *testing.T) {
	t.Parallel()

	th := open(t)
	ctx := context.Background()

	if err := th.AppendUser(ctx, "hello"); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	th.BeginTurn()
	if err := th.AppendAssistant(ctx, llm.Message{Content: "hi"}, nil, thread.TurnMeta{}); err != nil {
		t.Fatalf("AppendAssistant: %v", err)
	}
	err := th.AppendToolResult(ctx, "call-1", "read", nil, tool.Result{Content: "file contents"}, "", nil)
	if err != nil {
		t.Fatalf("AppendToolResult: %v", err)
	}

	got := th.History()
	if len(got) != 3 {
		t.Fatalf("History len = %d, want 3", len(got))
	}

	// Mutating the returned slice must not affect the thread's stored history.
	got[0].Content = "tampered"
	again := th.History()
	if again[0].Content != "hello" {
		t.Errorf("History()[0].Content = %q after external mutation, want unaffected %q", again[0].Content, "hello")
	}

	if got[0].Role != llm.RoleUser || got[1].Role != llm.RoleAssistant || got[2].Role != llm.RoleTool {
		t.Errorf("roles = %v, want [user assistant tool]", []llm.Role{got[0].Role, got[1].Role, got[2].Role})
	}
	if got[2].ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q, want call-1", got[2].ToolCallID)
	}
}

func TestAppendAssistantLogsRoleFromToolCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		want      event.Role
		toolCalls []llm.ToolCall
	}{
		{name: "no tool calls is an answer", toolCalls: nil, want: event.RoleAnswer},
		{
			name:      "tool calls make it a note",
			toolCalls: []llm.ToolCall{{ID: "1", Name: "read"}},
			want:      event.RoleNote,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			th := open(t)
			ctx := context.Background()
			th.BeginTurn()
			msg := llm.Message{Content: "hi", ToolCalls: tt.toolCalls}
			if err := th.AppendAssistant(ctx, msg, nil, thread.TurnMeta{}); err != nil {
				t.Fatalf("AppendAssistant: %v", err)
			}

			events, err := th.Log().Since(0)
			if err != nil {
				t.Fatalf("Since: %v", err)
			}
			last := events[len(events)-1]
			if last.Kind != event.KindAgent {
				t.Fatalf("last event kind = %q, want agent", last.Kind)
			}
			if last.Role != tt.want {
				t.Errorf("Role = %q, want %q", last.Role, tt.want)
			}
			if last.Text != "" {
				t.Errorf("Text = %q, want empty: the role marker carries no prose", last.Text)
			}
		})
	}
}

func TestSetStateLogsEvent(t *testing.T) {
	t.Parallel()

	th := open(t)
	ctx := context.Background()

	if err := th.SetState(ctx, event.StateWorking); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if th.State() != event.StateWorking {
		t.Errorf("State = %q, want working", th.State())
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 || events[0].Kind != event.KindState || events[0].State != event.StateWorking {
		t.Fatalf("events = %+v, want one KindState working event", events)
	}
}

func TestAppendCanceledContextDoesNothing(t *testing.T) {
	t.Parallel()

	th := open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := th.AppendUser(ctx, "hello"); err == nil {
		t.Fatal("AppendUser: want error on canceled ctx")
	}
	if len(th.History()) != 0 {
		t.Errorf("History len = %d, want 0 after canceled append", len(th.History()))
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events len = %d, want 0 after canceled append", len(events))
	}
}

func TestBeginTurnIncrements(t *testing.T) {
	t.Parallel()

	th := open(t)
	if th.Turn() != 0 {
		t.Fatalf("Turn = %d, want 0", th.Turn())
	}
	if got := th.BeginTurn(); got != 1 {
		t.Errorf("BeginTurn = %d, want 1", got)
	}
	if got := th.BeginTurn(); got != 2 {
		t.Errorf("BeginTurn = %d, want 2", got)
	}
	if th.Turn() != 2 {
		t.Errorf("Turn = %d, want 2", th.Turn())
	}
}

// A failed edit anchor cannot be diagnosed after the fact without the input
// the model actually sent.
func TestAppendToolResultLogsTheInput(t *testing.T) {
	t.Parallel()

	th, err := thread.Open(t.TempDir(), "t1", []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := th.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	input := json.RawMessage(`{"path":"a.go","old_string":"x"}`)
	err = th.AppendToolResult(t.Context(), "c1", "str_replace", input, tool.Result{Content: "ok"}, "", nil)
	if err != nil {
		t.Fatalf("AppendToolResult: %v", err)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var got string
	for _, ev := range events {
		if ev.Kind == event.KindTool {
			s, ok := ev.Detail["input"].(string)
			if !ok {
				t.Fatalf("tool event detail has no string input: %v", ev.Detail)
			}
			got = s
		}
	}
	if !strings.Contains(got, "old_string") {
		t.Fatalf("tool input not logged, got %q", got)
	}
}

// A failed call's arguments are the whole evidence for why it failed, and
// the successful call's bound is short enough to cut a degenerate emission
// off before the part that names it. Classifying this project's logged
// edit failures ran into exactly that: 13 malformed calls were all stored
// at the 2000-character bound with their tails gone.
func TestAppendToolResultKeepsAFailedCallsArgumentsWhole(t *testing.T) {
	t.Parallel()

	th, err := thread.Open(t.TempDir(), "t1", []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := th.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	// Long enough that the successful call's bound would cut it, and marked
	// at the end so the assertion reads the tail rather than the head.
	filler := strings.Repeat("Status: M2 in progress.\n", 400)
	input := json.RawMessage(`{"path":"a.go","old_string":"` + filler + `TAIL"}`)

	ctx := t.Context()
	if err := th.AppendToolResult(ctx, "c1", "str_replace", input,
		tool.Result{Content: "not found", IsError: true}, "", nil); err != nil {
		t.Fatalf("AppendToolResult: %v", err)
	}
	if err := th.AppendToolResult(ctx, "c2", "str_replace", input,
		tool.Result{Content: "ok"}, "", nil); err != nil {
		t.Fatalf("AppendToolResult: %v", err)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var logged []string
	for _, ev := range events {
		if ev.Kind == event.KindTool {
			s, ok := ev.Detail["input"].(string)
			if !ok {
				t.Fatalf("tool event detail has no string input: %v", ev.Detail)
			}
			logged = append(logged, s)
		}
	}

	if len(logged) != 2 {
		t.Fatalf("logged %d tool events, want 2", len(logged))
	}

	if !strings.HasSuffix(logged[0], `TAIL"}`) {
		t.Errorf("failed call's input ends %q, want the arguments whole", tailOf(logged[0]))
	}

	if len(logged[1]) >= len(logged[0]) {
		t.Errorf("successful call kept %d bytes against the failed call's %d, want the shorter bound",
			len(logged[1]), len(logged[0]))
	}
}

func tailOf(s string) string {
	const n = 40
	if len(s) <= n {
		return s
	}

	return s[len(s)-n:]
}

// A thread's goal is its first prompt, and a rewrite appends rather than
// editing, so what the goal was at any turn stays readable. GoalFrom is
// what a resumed thread reads it back with, since Open never replays a log.
func TestGoalIsTheFirstPromptUntilRewritten(t *testing.T) {
	t.Parallel()

	th := open(t)
	ctx := t.Context()

	if got := th.Goal(); got != "" {
		t.Fatalf("Goal on a fresh thread = %q, want empty", got)
	}

	if err := th.AppendUser(ctx, "make the lease TTL configurable"); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}

	if got := th.Goal(); got != "make the lease TTL configurable" {
		t.Fatalf("Goal = %q, want the first prompt", got)
	}

	if err := th.AppendUser(ctx, "start in internal/lease"); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}

	if got := th.Goal(); got != "make the lease TTL configurable" {
		t.Errorf("Goal = %q, want a later prompt to leave it alone", got)
	}

	if err := th.SetGoal(ctx, "make every timeout configurable"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	if got := thread.GoalFrom(events); got != "make every timeout configurable" {
		t.Errorf("GoalFrom = %q, want the rewritten goal", got)
	}
}

// A run stopped on a bound keeps the files it wrote; without this it kept
// nothing else, because the event log truncates tool inputs and stores
// assistant text as streamed chunks, so it cannot rebuild what the model
// was sent.
func TestReopenRestoresTheTranscript(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := t.Context()

	first, err := thread.Open(dir, "t1", []string{"/repo"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	first.BeginTurn()
	if err := first.AppendUser(ctx, "fix the parser"); err != nil {
		t.Fatalf("user: %v", err)
	}
	calls := []llm.ToolCall{
		{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)},
		// A run stops on exactly this, so the transcript that explains why
		// has to survive being written down.
		{ID: "c2", Name: "read", Input: json.RawMessage(`{path: not json`)},
	}
	if err := first.AppendAssistant(ctx,
		llm.Message{Content: "reading", ToolCalls: calls}, nil, thread.TurnMeta{}); err != nil {
		t.Fatalf("assistant: %v", err)
	}
	if err := first.AppendToolResult(ctx, "c1", "read",
		json.RawMessage(`{"path":"a.go"}`), tool.Result{Content: "package a"}, "", nil); err != nil {
		t.Fatalf("tool: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := thread.Open(dir, "t1", []string{"/repo"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	assertTranscript(t, second)
}

func assertTranscript(t *testing.T, th *thread.Thread) {
	t.Helper()

	got := th.History()
	if len(got) != 3 {
		t.Fatalf("History = %d messages, want 3", len(got))
	}
	if got[0].Role != llm.RoleUser || got[0].Content != "fix the parser" {
		t.Errorf("first message = %+v, want the user prompt", got[0])
	}
	if len(got[1].ToolCalls) != 2 || string(got[1].ToolCalls[0].Input) != `{"path":"a.go"}` {
		t.Errorf("assistant tool calls = %+v, want both calls whole", got[1].ToolCalls)
	}
	if string(got[1].ToolCalls[1].Input) != `{path: not json` {
		t.Errorf("malformed input = %q, want the bytes the model emitted", got[1].ToolCalls[1].Input)
	}
	if got[2].Role != llm.RoleTool || got[2].ToolCallID != "c1" {
		t.Errorf("third message = %+v, want the tool result", got[2])
	}
	if th.Turn() != 1 {
		t.Errorf("Turn = %d, want 1 so the next turn does not reuse it", th.Turn())
	}
}
