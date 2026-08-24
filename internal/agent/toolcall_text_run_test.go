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

// unparsableTurn trips the detection (a marker followed by a registered
// tool name) while carrying nothing the parser can read back, which is the
// case recovery must not paper over.
func unparsableTurn() fake.Turn {
	return fake.Turn{
		Text:       []string{"calling <tool_call> echo now, roughly"},
		StopReason: llm.StopEndTurn,
	}
}

// A rendered call is the model calling the tool in a dialect the provider
// did not parse, so the loop reads it and runs it. What this must not lose
// is the outcome the check was built for: before it existed, a turn whose
// only content was markup reported StopComplete having changed nothing, so
// a run that did nothing looked exactly like one that succeeded.
func TestRun_ToolCallWrittenAsTextIsRecovered(t *testing.T) {
	t.Parallel()

	local := fake.New("local", asTextTurn(),
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	loop := agent.New(tiers(local, fake.New("hosted")),
		tool.NewRegistry(echoTool{name: "echo"}), permission.AllowAll())

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Stop != agent.StopComplete {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopComplete)
	}

	if out.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want the rendered call to have run", out.ToolCalls)
	}

	// Recorded, because a tier that needs this has a templating problem and
	// that should be visible rather than silently absorbed.
	if out.RecoveredCalls != 1 {
		t.Errorf("RecoveredCalls = %d, want 1", out.RecoveredCalls)
	}
}

// Markup the parser cannot read back is prose, and a run that ends on it
// has still done nothing. That is the outcome the original check was built
// for and recovery must not lose it.
func TestRun_UnreadableMarkupIsStillNotSuccess(t *testing.T) {
	t.Parallel()

	loop := agent.New(tiers(fake.New("local", unparsableTurn()), fake.New("hosted", unparsableTurn())),
		tool.NewRegistry(echoTool{name: "echo"}), permission.AllowAll())

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Stop != agent.StopMalformedTool {
		t.Errorf("Stop = %q, want %q", out.Stop, agent.StopMalformedTool)
	}

	if out.ToolCalls != 0 || out.RecoveredCalls != 0 {
		t.Errorf("calls = %d, recovered = %d; nothing was readable", out.ToolCalls, out.RecoveredCalls)
	}
}
