package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

type verifyOutcome struct {
	feedback string
	// verdict overrides ok, for the cases ok cannot express. Empty means
	// ok decides.
	verdict agent.GateVerdict
	ok      bool
}

type verifyCall struct {
	changes []tool.Change
}

// stubVerifier scripts a sequence of Verify outcomes; a call past the end
// of the script passes with no feedback.
type stubVerifier struct {
	script []verifyOutcome
	calls  []verifyCall
}

func (v *stubVerifier) Verify(_ context.Context, changes []tool.Change) (string, agent.GateVerdict) {
	v.calls = append(v.calls, verifyCall{changes: changes})
	if idx := len(v.calls) - 1; idx < len(v.script) {
		out := v.script[idx]
		if out.verdict != "" {
			return out.feedback, out.verdict
		}
		if out.ok {
			return out.feedback, agent.VerdictPass
		}

		return out.feedback, agent.VerdictFailed
	}

	return "", agent.VerdictPass
}

type changeTool struct {
	name    string
	changes []tool.Change
}

func (c changeTool) Name() string          { return c.name }
func (changeTool) Description() string     { return "records a change" }
func (changeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (c changeTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok", Changes: c.changes}, nil
}

func gateEventPasses(t *testing.T, th *thread.Thread) []bool {
	t.Helper()

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var out []bool

	for i := range events {
		if events[i].Kind != event.KindGate {
			continue
		}

		pass, ok := events[i].Detail["pass"].(bool)
		if !ok {
			t.Fatalf("gate event Detail[%q] = %+v, want a bool", "pass", events[i].Detail["pass"])
		}

		out = append(out, pass)
	}

	return out
}

func TestRun_NoVerifierBehaviorUnchanged(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete || out.Turns != 1 {
		t.Fatalf("Outcome = %+v, want complete after 1 turn", out)
	}

	for _, k := range eventKinds(t, th) {
		if k == event.KindGate {
			t.Fatalf("kinds carry a gate event with no verifier configured")
		}
	}
}

func TestRun_VerifierFailureAppendsFeedbackAndRetries(t *testing.T) {
	t.Parallel()

	local := fake.New("local",
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
		fake.Turn{Text: []string{"done again"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	v := &stubVerifier{script: []verifyOutcome{{feedback: "fix the import", ok: false}}}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithVerifier(v))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete {
		t.Fatalf("Stop = %q, want complete", out.Stop)
	}
	if out.Turns != 2 {
		t.Fatalf("Turns = %d, want 2", out.Turns)
	}
	if len(v.calls) != 2 {
		t.Fatalf("Verify called %d times, want 2", len(v.calls))
	}

	found := false

	for _, m := range th.History() {
		if m.Role == llm.RoleUser && m.Content == "fix the import" {
			found = true
		}
	}

	if !found {
		t.Errorf("history did not carry the verification feedback as a user turn: %+v", th.History())
	}

	if got := gateEventPasses(t, th); len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("gate event passes = %v, want [false true]", got)
	}
}

func TestRun_VerifierPassSendsNothingToModel(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	v := &stubVerifier{}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithVerifier(v))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopComplete || out.Turns != 1 {
		t.Fatalf("Outcome = %+v, want complete after 1 turn", out)
	}
	if len(local.Requests()) != 1 {
		t.Fatalf("local Requests = %d, want 1: a pass must not trigger another turn", len(local.Requests()))
	}

	if got := gateEventPasses(t, th); len(got) != 1 || !got[0] {
		t.Fatalf("gate event passes = %v, want [true]", got)
	}
}

func TestRun_VerifyRoundBoundTrips(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	v := &stubVerifier{script: []verifyOutcome{
		{feedback: "still broken", ok: false},
		{feedback: "still broken", ok: false},
	}}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
		agent.WithVerifier(v), agent.WithMaxVerifyRounds(1))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopVerifyFailed {
		t.Fatalf("Stop = %q, want verify_failed", out.Stop)
	}
	if len(v.calls) != 1 {
		t.Fatalf("Verify called %d times, want 1", len(v.calls))
	}

	kinds := eventKinds(t, th)
	if kinds[len(kinds)-2] != event.KindError {
		t.Fatalf("second-to-last event kind = %q, want error", kinds[len(kinds)-2])
	}
}

func TestRun_OnlyTrimmedFeedbackReachesModel(t *testing.T) {
	t.Parallel()

	local := fake.New("local",
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
		fake.Turn{Text: []string{"done again"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})

	const trimmed = "go-test failed: TestFoo: pkg/foo.go:10: boom"

	const untrimmedRaw = "raw untrimmed stack trace nobody should see"

	v := &stubVerifier{script: []verifyOutcome{{feedback: trimmed, ok: false}}}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithVerifier(v))

	if _, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	found := false

	for _, req := range local.Requests() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, untrimmedRaw) {
				t.Fatalf("request carried untrimmed text: %q", m.Content)
			}
			if m.Content == trimmed {
				found = true
			}
		}
	}

	if !found {
		t.Errorf("no request carried the trimmed feedback %q", trimmed)
	}
}

func TestRun_ChangesAccumulateAcrossToolsAndTurns(t *testing.T) {
	t.Parallel()

	call1 := llm.ToolCall{ID: "1", Name: "change-a", Input: json.RawMessage(`{}`)}
	call2 := llm.ToolCall{ID: "2", Name: "change-b", Input: json.RawMessage(`{}`)}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call1}, StopReason: llm.StopToolUse},
		fake.Turn{ToolCalls: []llm.ToolCall{call2}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(
		changeTool{name: "change-a", changes: []tool.Change{{Path: "a.go", Added: 1}}},
		changeTool{name: "change-b", changes: []tool.Change{{Path: "b.go", Added: 2}}},
	)
	v := &stubVerifier{}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithVerifier(v))

	if _, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(v.calls) != 1 {
		t.Fatalf("Verify called %d times, want 1", len(v.calls))
	}

	got := v.calls[0].changes
	if len(got) != 2 || got[0].Path != "a.go" || got[1].Path != "b.go" {
		t.Fatalf("changes = %+v, want a.go then b.go", got)
	}
}

// Ending on a bound with edited files and no verification is the worst case:
// changed code and no signal.
func TestRun_BoundedRunStillVerifiesWhatItChanged(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{ID: "1", Name: "changer", Input: rawJSON(t, map[string]any{"a": 1})}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse},
	)
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(changeTool{name: "changer", changes: []tool.Change{{Path: "a.go", Added: 2}}})
	v := &stubVerifier{script: []verifyOutcome{{feedback: "build failed: undefined: filepath", ok: false}}}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
		agent.WithMaxTurns(1), agent.WithVerifier(v))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopMaxTurns {
		t.Fatalf("Stop = %q, want max_turns", out.Stop)
	}
	if len(v.calls) == 0 {
		t.Fatal("a bounded run left changed files unverified")
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var logged bool
	for _, ev := range events {
		if abandoned, ok := ev.Detail["abandoned"].(bool); ok && abandoned && ev.Kind == event.KindGate {
			logged = true
		}
	}
	if !logged {
		t.Fatal("the abandoned change set's gate result was never logged")
	}
}

// TestRun_UnattributedVerdictStopsForScheduler covers the case two parallel
// dogfood lanes lost their work to: a gate failure in packages the run never
// touched. The run must stop rather than spend its remaining turns on it, and
// the model must never be handed the feedback, since nothing it can write
// changes the answer.
func TestRun_UnattributedVerdictStopsForScheduler(t *testing.T) {
	t.Parallel()

	local := fake.New("local",
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
		fake.Turn{Text: []string{"done again"}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")

	const feedback = "the tree fails a gate this run cannot have caused"

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	v := &stubVerifier{script: []verifyOutcome{{feedback: feedback, verdict: agent.VerdictUnattributed}}}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
		agent.WithVerifier(v), agent.WithMaxVerifyRounds(5))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopTreeState {
		t.Fatalf("Stop = %q, want tree_state", out.Stop)
	}
	if len(local.Requests()) != 1 {
		t.Fatalf("local Requests = %d, want 1: an unattributed failure must not cost a turn", len(local.Requests()))
	}

	for _, req := range local.Requests() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, feedback) {
				t.Fatalf("unattributed feedback reached the model: %q", m.Content)
			}
		}
	}
}
