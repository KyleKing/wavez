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

// The same prefix is not the same cost: it is a third of what a fast turn
// can use and under 2% of a hosted one. The tiers are served by different
// processes and keep separate prefix caches, so showing each one a
// different surface costs nothing.
func TestRun_ShowsTheFastTierItsOwnToolSurface(t *testing.T) {
	t.Parallel()

	prefix := agent.Prefix{
		System: "you are wavez",
		Tools: []llm.ToolSpec{
			{Name: "search", Description: "reads", Schema: json.RawMessage(`{}`)},
			{Name: "shell", Description: "runs", Schema: json.RawMessage(`{}`)},
		},
		FastTools: []llm.ToolSpec{
			{Name: "search", Description: "reads", Schema: json.RawMessage(`{}`)},
		},
	}

	for _, tc := range []struct {
		tier router.Choice
		want int
	}{
		{tier: router.ChoiceFast, want: 1},
		{tier: router.ChoiceBalanced, want: 2},
	} {
		t.Run(string(tc.tier), func(t *testing.T) {
			t.Parallel()

			provider := fake.New(string(tc.tier),
				fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})

			loop := agent.New(tiers(provider, fake.New("deep")),
				tool.NewRegistry(readOnlyTool{}), permission.AllowAll())

			if _, err := loop.Run(context.Background(), newThread(t), prefix,
				"say something", router.Input{Override: tc.tier}); err != nil {
				t.Fatalf("Run: %v", err)
			}

			reqs := provider.Requests()
			if len(reqs) == 0 {
				t.Fatal("no request reached the provider")
			}

			if got := len(reqs[0].Tools); got != tc.want {
				t.Errorf("%s was shown %d tools, want %d: %+v", tc.tier, got, tc.want, reqs[0].Tools)
			}
		})
	}
}
