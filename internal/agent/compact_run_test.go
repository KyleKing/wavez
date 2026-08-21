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
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

// bulkTool returns one oversized result per call, so a couple of calls carry
// a history past the compaction trigger's share of the 8k local budget.
type bulkTool struct{ name string }

func (b bulkTool) Name() string          { return b.name }
func (bulkTool) Description() string     { return "returns a lot of output" }
func (bulkTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

const (
	bulkLine        = "a line of tool output\n"
	bulkLines       = 1200
	bulkResultChars = len(bulkLine) * bulkLines
)

func (bulkTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: strings.Repeat(bulkLine, bulkLines)}, nil
}

// bulkTurns scripts n distinct calls; distinct inputs keep the loop's
// repeated-call detector out of the way.
func bulkTurns(n int) []fake.Turn {
	out := make([]fake.Turn, 0, n+1)
	for i := range n {
		out = append(out, fake.Turn{
			ToolCalls: []llm.ToolCall{{
				ID:    fmt.Sprintf("c%d", i),
				Name:  "bulk",
				Input: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
			}},
			StopReason: llm.StopToolUse,
		})
	}

	return append(out, fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
}

// TestRun_CompactionTrimsAndThenAppends pins both halves of the compaction
// decision: it must actually shrink an oversized request, and the request
// after it must extend the compacted prefix rather than recompute it, since
// editing a cached prefix measured 5-7x the cost of appending to one.
func TestRun_CompactionTrimsAndThenAppends(t *testing.T) {
	t.Parallel()

	local := fake.New("local", bulkTurns(4)...)
	loop := agent.New(tiers(local, fake.New("hosted")), tool.NewRegistry(bulkTool{name: "bulk"}),
		permission.AllowAll(),
		agent.WithCompaction(thread.CompactOptions{KeepLines: 5, MaxToolAge: 1, DedupeReads: true},
			agent.DefaultCompactTrigger))

	hint := router.Input{Override: router.ChoiceFast}

	out, err := loop.Run(context.Background(), newThread(t), basicPrefix(), "go", hint)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.TokensCompacted <= 0 {
		t.Fatalf("TokensCompacted = %d, want > 0; compaction never ran", out.TokensCompacted)
	}

	reqs := local.Requests()
	if len(reqs) < 4 {
		t.Fatalf("len(Requests) = %d, want at least 4", len(reqs))
	}

	// Four bulk results are over 100k characters uncompacted; the final
	// request must be a fraction of that.
	if got := chars(reqs[len(reqs)-1]); got > bulkResultChars {
		t.Errorf("final request is %d chars, want under %d; compaction did not trim", got, bulkResultChars)
	}

	for i := 1; i < len(reqs); i++ {
		prev, cur := reqs[i-1], reqs[i]
		for j := range prev.Messages {
			if cur.Messages[j].Content != prev.Messages[j].Content {
				t.Fatalf("message %d changed between request %d and %d; the cached prefix was rewritten",
					j, i-1, i)
			}
		}
	}
}

func chars(r llm.Request) int {
	total := 0
	for _, m := range r.Messages {
		total += len(m.Content)
	}

	return total
}
