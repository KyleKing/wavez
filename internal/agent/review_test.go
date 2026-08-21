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

const reviewTask = "rewrite the empty state"

// stubReviewer scripts a sequence of verdicts; a call past the end of the
// script returns ReviewOK.
type stubReviewer struct {
	script []agent.Verdict
	calls  []agent.Review
}

func (r *stubReviewer) Review(_ context.Context, rv agent.Review) agent.Verdict {
	r.calls = append(r.calls, rv)
	if idx := len(r.calls) - 1; idx < len(r.script) {
		return r.script[idx]
	}

	return agent.Verdict{Result: agent.ReviewOK}
}

// editScript is one tool call that changes a file followed by n end-turn
// replies, which is the shape of a run that edits once and is then told to
// try again.
func editScript(n int) []fake.Turn {
	call := llm.ToolCall{ID: "1", Name: "editor", Input: json.RawMessage(`{}`)}
	out := make([]fake.Turn, 0, n+1)
	out = append(out, fake.Turn{ToolCalls: []llm.ToolCall{call}, StopReason: llm.StopToolUse})

	for range n {
		out = append(out, fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	}

	return out
}

func reviewEvents(t *testing.T, th *thread.Thread) []event.Event {
	t.Helper()

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var out []event.Event

	for i := range events {
		if events[i].Kind == event.KindReview {
			out = append(out, events[i])
		}
	}

	return out
}

func userTurns(th *thread.Thread) []string {
	var out []string

	for _, m := range th.History() {
		if m.Role == llm.RoleUser {
			out = append(out, m.Content)
		}
	}

	return out
}

func TestRun_Review(t *testing.T) {
	t.Parallel()

	objection := agent.Verdict{Result: agent.ReviewObjection, Note: "it kept both strings instead of branching"}
	tooLarge := agent.Verdict{Result: agent.ReviewSkipped, Note: "the diff is 90000 bytes, over the 6000-byte budget"}

	tests := []reviewCase{
		{
			name:        "passes",
			script:      []agent.Verdict{{Result: agent.ReviewOK}},
			replies:     1,
			wantReviews: 1,
			wantTurns:   2,
			wantVerdict: agent.Verdict{Result: agent.ReviewOK},
		},
		{
			name:        "objects once then the model fixes it",
			script:      []agent.Verdict{objection, {Result: agent.ReviewOK}},
			replies:     2,
			wantReviews: 2,
			wantTurns:   3,
			wantFedBack: true,
			wantVerdict: agent.Verdict{Result: agent.ReviewOK},
		},
		{
			name:        "objects twice and the run still completes carrying the objection",
			script:      []agent.Verdict{objection, objection},
			replies:     2,
			wantReviews: 2,
			wantTurns:   3,
			wantFedBack: true,
			wantVerdict: objection,
		},
		{
			name:        "a diff too large to review is not a pass",
			script:      []agent.Verdict{tooLarge},
			replies:     1,
			wantReviews: 1,
			wantTurns:   2,
			wantVerdict: tooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			local := fake.New("local", editScript(tt.replies)...)
			th := newThread(t)
			reg := tool.NewRegistry(changeTool{name: "editor", changes: []tool.Change{{Path: "a.go", Added: 1}}})
			rv := &stubReviewer{script: tt.script}
			loop := agent.New(tiers(local, fake.New("hosted")), reg, permission.AllowAll(),
				agent.WithVerifier(&stubVerifier{}), agent.WithReviewer(rv))

			out, err := loop.Run(context.Background(), th, basicPrefix(), reviewTask, router.Input{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			tt.checkOutcome(t, out, rv)
			tt.checkThread(t, th, objection.Note)
		})
	}
}

type reviewCase struct {
	name        string
	wantVerdict agent.Verdict
	script      []agent.Verdict
	replies     int
	wantReviews int
	wantTurns   int
	wantFedBack bool
}

func (c reviewCase) checkOutcome(t *testing.T, out agent.Outcome, rv *stubReviewer) {
	t.Helper()

	if out.Stop != agent.StopComplete {
		t.Fatalf("Stop = %q, want complete: a review objection must never fail a run", out.Stop)
	}
	if out.Turns != c.wantTurns {
		t.Errorf("Turns = %d, want %d", out.Turns, c.wantTurns)
	}
	if out.Review != c.wantVerdict {
		t.Errorf("Outcome.Review = %+v, want %+v", out.Review, c.wantVerdict)
	}
	if len(rv.calls) != c.wantReviews {
		t.Fatalf("Review called %d times, want %d", len(rv.calls), c.wantReviews)
	}
	if got := rv.calls[0].Task; got != reviewTask {
		t.Errorf("Review.Task = %q, want the run's task text", got)
	}
	if got := rv.calls[0].Changes; len(got) != 1 || got[0].Path != "a.go" {
		t.Errorf("Review.Changes = %+v, want the run's accumulated changes", got)
	}
}

func (c reviewCase) checkThread(t *testing.T, th *thread.Thread, objection string) {
	t.Helper()

	fedBack := false

	for _, content := range userTurns(th) {
		if strings.Contains(content, objection) {
			fedBack = true
		}
	}

	if fedBack != c.wantFedBack {
		t.Errorf("objection fed back as a turn = %v, want %v: %q", fedBack, c.wantFedBack, userTurns(th))
	}

	events := reviewEvents(t, th)
	if len(events) != c.wantReviews {
		t.Fatalf("review events = %d, want %d", len(events), c.wantReviews)
	}

	last := events[len(events)-1]
	if got := last.Detail["result"]; got != string(c.wantVerdict.Result) {
		t.Errorf("last review event result = %v, want %q", got, c.wantVerdict.Result)
	}
	if c.wantVerdict.Note != "" && !strings.Contains(last.Text, c.wantVerdict.Note) {
		t.Errorf("last review event text = %q, want it to carry %q", last.Text, c.wantVerdict.Note)
	}
}

func TestRun_ReviewSkippedWhenNothingToJudge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		verifier *stubVerifier
		wantStop agent.Stop
		script   []fake.Turn
	}{
		{
			name:     "no files changed",
			verifier: &stubVerifier{},
			wantStop: agent.StopComplete,
			script:   []fake.Turn{{Text: []string{"nothing to do"}, StopReason: llm.StopEndTurn}},
		},
		{
			name:     "gates failed",
			verifier: &stubVerifier{script: []verifyOutcome{{feedback: "build failed", ok: false}}},
			wantStop: agent.StopVerifyFailed,
			script:   editScript(1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			th := newThread(t)
			reg := tool.NewRegistry(changeTool{name: "editor", changes: []tool.Change{{Path: "a.go", Added: 1}}})
			rv := &stubReviewer{}
			loop := agent.New(tiers(fake.New("local", tt.script...), fake.New("hosted")), reg, permission.AllowAll(),
				agent.WithVerifier(tt.verifier), agent.WithReviewer(rv), agent.WithMaxVerifyRounds(1))

			out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if out.Stop != tt.wantStop {
				t.Errorf("Stop = %q, want %q", out.Stop, tt.wantStop)
			}
			if out.Review.Result != "" {
				t.Errorf("Outcome.Review = %+v, want the zero verdict", out.Review)
			}
			if len(rv.calls) != 0 {
				t.Errorf("Review called %d times, want 0", len(rv.calls))
			}
			if len(reviewEvents(t, th)) != 0 {
				t.Error("a review event was logged with nothing reviewed")
			}
		})
	}
}
