package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// readOnlyTool stands for the search-and-read calls a run makes before it
// edits anything.
type readOnlyTool struct{}

func (readOnlyTool) Name() string            { return "search" }
func (readOnlyTool) Description() string     { return "reads" }
func (readOnlyTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (readOnlyTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "a result"}, nil
}

// A run that reads for many turns and changes nothing is told to start.
// Two dogfood runs spent their whole budget that way.
func TestRun_NudgesARunThatHasChangedNothing(t *testing.T) {
	t.Parallel()

	turns := make([]fake.Turn, 0, 9)
	for i := range 8 {
		turns = append(turns, fake.Turn{
			ToolCalls: []llm.ToolCall{{
				ID: fmt.Sprintf("c%d", i), Name: "search",
				Input: json.RawMessage(fmt.Sprintf(`{"q":%d}`, i)),
			}},
			StopReason: llm.StopToolUse,
		})
	}
	provider := fake.New("balanced", turns...)
	prefix := basicPrefix()
	prefix.Tools = append(prefix.Tools,
		llm.ToolSpec{Name: "str_replace", Description: "edits", Schema: json.RawMessage(`{}`)})

	loop := agent.New(tiers(provider, fake.New("deep")),
		tool.NewRegistry(readOnlyTool{}), permission.AllowAll(),
		agent.WithTurnsBeforeNudge(3), agent.WithMaxTurns(8))

	// Wording the fatal no-change rule reads as a question, which must not
	// keep a run holding an editing tool from being told to start.
	if _, err := loop.Run(context.Background(), newThread(t), prefix,
		"Count the tool calls that failed", router.Input{Override: router.ChoiceBalanced}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var nudges int
	for _, req := range provider.Requests() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "changed no file in") {
				nudges++

				break
			}
		}
	}

	if nudges == 0 {
		t.Fatal("a run that changed nothing for 8 turns was never told to start")
	}
}
