package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// offerTurn is the turn shape observed from qwen3:8b on this repo: the model
// summarizes edits it made and closes by offering to verify them.
func offerTurn() fake.Turn {
	return fake.Turn{
		Text: []string{
			"I have made the requested changes.\n\n",
			"The changes should now pass all relevant tests. Would you like me to run the tests to verify this?",
		},
		StopReason: llm.StopEndTurn,
	}
}

// announceTurn is the turn shape observed from qwen3:8b on this repo: the
// model narrates a plan and ends the turn without making the call.
func announceTurn() fake.Turn {
	return fake.Turn{
		Text: []string{
			"The error indicates a permission issue.\n\n",
			"I'll start by running the `hk check --all` command to see what is failing.",
		},
		StopReason: llm.StopEndTurn,
	}
}

// TestRun_AnnouncingAnActionIsNotTakingIt pins the second half of the same
// failure: a run whose closing turn announced a command and ran none still
// reported StopComplete.
func TestRun_AnnouncingAnActionIsNotTakingIt(t *testing.T) {
	t.Parallel()

	local := fake.New("local", announceTurn())
	hosted := fake.New("hosted", announceTurn())
	loop := agent.New(tiers(local, hosted), tool.NewRegistry(echoTool{name: "echo"}), permission.AllowAll())

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Stop != agent.StopAnnouncedNotDone {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopAnnouncedNotDone)
	}
}

// TestRun_OfferingToActIsNotSuccess pins the outcome a real run produced:
// the model edited two files wrongly, closed by offering to run the tests,
// and the run reported StopComplete, so an unverified offer read as
// finished work.
func TestRun_OfferingToActIsNotSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		local    *fake.Provider
		hosted   *fake.Provider
		name     string
		wantStop agent.Stop
	}{
		{
			name:  "acting after the critique completes the run",
			local: fake.New("local", offerTurn()),
			hosted: fake.New("hosted", fake.Turn{
				Text: []string{"ran the tests, all pass"}, StopReason: llm.StopEndTurn,
			}),
			wantStop: agent.StopComplete,
		},
		{
			name:     "offering again after escalating stops the run",
			local:    fake.New("local", offerTurn()),
			hosted:   fake.New("hosted", offerTurn()),
			wantStop: agent.StopAskedInProse,
		},
		{
			name: "a closing question that offers nothing still completes",
			local: fake.New("local", fake.Turn{
				Text:       []string{"I renamed DefaultTTL to TTL. Was that the rename you meant?"},
				StopReason: llm.StopEndTurn,
			}),
			hosted:   fake.New("hosted"),
			wantStop: agent.StopComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			th := newThread(t)
			reg := tool.NewRegistry(echoTool{name: "echo"})
			loop := agent.New(tiers(tt.local, tt.hosted), reg, permission.AllowAll())

			out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if out.Stop != tt.wantStop {
				t.Errorf("Stop = %q, want %q", out.Stop, tt.wantStop)
			}
		})
	}
}

// TestRun_EditAttemptedWithNoChangeIsNotComplete pins the shape from
// dogfood.md: a run whose edit tool errored on every call and whose closing
// turn reported success anyway, having changed nothing, must not report
// StopComplete on the model's own account of itself.
func TestRun_EditAttemptedWithNoChangeIsNotComplete(t *testing.T) {
	t.Parallel()

	editCall := llm.ToolCall{ID: "1", Name: "str_replace", Input: json.RawMessage(`{"path":"x.go"}`)}
	claimsDone := fake.Turn{
		Text:       []string{"The rename is applied across the package."},
		StopReason: llm.StopEndTurn,
	}

	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{editCall}, StopReason: llm.StopToolUse},
		claimsDone,
	)
	hosted := fake.New("hosted", claimsDone)
	reg := tool.NewRegistry(erroringTool{echoTool: echoTool{name: "str_replace"}})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "rename DefaultTTL to TTL", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Stop != agent.StopStagnant {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopStagnant)
	}
}

// TestRun_QuestionTaskCompletesWithZeroChanges proves the new zero-change
// check does not fire on a task that never asked for an edit: reading a
// file to answer a question changes nothing, and that is success, not a
// stagnant run.
func TestRun_QuestionTaskCompletesWithZeroChanges(t *testing.T) {
	t.Parallel()

	searchCall := llm.ToolCall{ID: "1", Name: "search", Input: json.RawMessage(`{"query":"guard rules"}`)}
	local := fake.New("local",
		fake.Turn{ToolCalls: []llm.ToolCall{searchCall}, StopReason: llm.StopToolUse},
		fake.Turn{Text: []string{"internal/agent/loop.go defines the guard rules."}, StopReason: llm.StopEndTurn},
	)
	hosted := fake.New("hosted")
	reg := tool.NewRegistry(echoTool{name: "search"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "which file defines the guard rules",
		router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Stop != agent.StopComplete {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopComplete)
	}
}

// A task whose wording asks for a change is edit-shaped even when the model
// never reaches for an edit tool, so a run that only searched and then
// claimed the rename is done ends stagnant rather than complete.
func TestRun_ClaimingAnEditNeverAttemptedIsNotComplete(t *testing.T) {
	t.Parallel()

	claimsDone := fake.Turn{
		Text:       []string{"Renamed firstDir to primaryDir across the daemon package."},
		StopReason: llm.StopEndTurn,
	}
	local := fake.New("local", claimsDone)
	hosted := fake.New("hosted", claimsDone)
	reg := tool.NewRegistry(echoTool{name: "search"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(),
		"Rename the unexported function firstDir in internal/daemon/thread.go to primaryDir.", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Stop != agent.StopStagnant {
		t.Errorf("Stop = %q, want %q (%s)", out.Stop, agent.StopStagnant, out.Reason)
	}
}
