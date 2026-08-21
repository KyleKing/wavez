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

// asTextTurn is the turn shape observed from qwen3-coder-30b through
// OpenRouter: the model narrates, renders the call as markup, and the
// provider reports an ordinary end of turn with no tool calls attached.
func asTextTurn() fake.Turn {
	return fake.Turn{
		Text: []string{
			"I'll create the file.\n\n",
			"<function=echo>\n<parameter=a>\n1\n</parameter>\n</function>",
		},
		StopReason: llm.StopEndTurn,
	}
}

// TestRun_ToolCallWrittenAsTextIsNotSuccess pins the outcome that mattered:
// before this check, a run whose only turn wrote its call as prose reported
// StopComplete with zero tool calls, so a run that changed nothing looked
// exactly like a run that succeeded.
func TestRun_ToolCallWrittenAsTextIsNotSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		local    *fake.Provider
		hosted   *fake.Provider
		wantStop agent.Stop
	}{
		{
			name:     "recovering after the critique completes the run",
			local:    fake.New("local", asTextTurn()),
			hosted:   fake.New("hosted", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn}),
			wantStop: agent.StopComplete,
		},
		{
			name:     "doing it again after escalating stops the run",
			local:    fake.New("local", asTextTurn()),
			hosted:   fake.New("hosted", asTextTurn()),
			wantStop: agent.StopMalformedTool,
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

			if out.ToolCalls != 0 {
				t.Errorf("ToolCalls = %d, want 0; nothing was actually called", out.ToolCalls)
			}
		})
	}
}
