package agent_test

import (
	"context"
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
	loop := agent.New(local, hosted, tool.NewRegistry(echoTool{name: "echo"}), permission.AllowAll())

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
			loop := agent.New(tt.local, tt.hosted, reg, permission.AllowAll())

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
