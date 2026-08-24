package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// A turn with no prose and no tool call is a failure of the call, not a
// model choosing to stay silent. Measured against `stealth/ox-alpha`, six
// hosted runs in a row ended in two turns with no tool calls and no spend,
// because the model is a reasoning model whose whole output arrived in a
// field the wire did not read: every hosted turn looked like a shrug, and
// the run was failed for talking without acting.
func TestRun_NamesAnEmptyTurnRatherThanBlamingTheModel(t *testing.T) {
	t.Parallel()

	empty := fake.Turn{
		StopReason: llm.StopEndTurn,
		Usage:      &llm.Usage{OutputTokens: 900, ReasoningBytes: 3400},
	}

	// One empty turn escalates; the tier above has nowhere to go.
	loop := agent.New(tiers(fake.New("balanced", empty), fake.New("deep", empty)),
		tool.NewRegistry(readOnlyTool{}), permission.AllowAll())

	outcome, err := loop.Run(context.Background(), newThread(t), basicPrefix(),
		"Edit a.go", router.Input{Override: router.ChoiceBalanced})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if outcome.Stop != agent.StopEmptyTurn {
		t.Fatalf("Stop = %q, want %q: %s", outcome.Stop, agent.StopEmptyTurn, outcome.Reason)
	}

	for _, want := range []string{"no text and no tool call", "3400 bytes on reasoning"} {
		if !strings.Contains(outcome.Reason, want) {
			t.Errorf("Reason = %q, want it to carry %q", outcome.Reason, want)
		}
	}
}
