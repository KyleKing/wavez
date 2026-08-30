package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// stubFinisher records what the loop handed it and answers with a fixed
// finding.
type stubFinisher struct {
	got agent.Finish
}

func (s *stubFinisher) Check(_ context.Context, f agent.Finish) ([]string, error) {
	s.got = f

	return []string{"named path does not exist: internal/agent/toolcall.go"}, nil
}

// editing stands for the tool a run reaches for, since a run that changes
// nothing never reaches the finish checks at all.
type editing struct{}

func (editing) Name() string            { return "str_replace" }
func (editing) Description() string     { return "edits" }
func (editing) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (editing) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "a.go: +1 -0 lines", Changes: []tool.Change{{Path: "a.go", Added: 1}}}, nil
}

// The finish checks bound a run that completed, and they never reach the
// model: a finding handed back would make them the reviewer they exist to
// replace, and the reviewer objected to correct diffs.
func TestRun_FinishChecksBoundACompletedRunWithoutTellingIt(t *testing.T) {
	t.Parallel()

	provider := fake.New("balanced",
		fake.Turn{
			ToolCalls:  []llm.ToolCall{{ID: "c1", Name: "str_replace", Input: json.RawMessage(`{}`)}},
			StopReason: llm.StopToolUse,
		},
		fake.Turn{Text: []string{"Done, see `internal/agent/toolcall.go`."}, StopReason: llm.StopEndTurn},
	)

	finisher := &stubFinisher{}
	loop := agent.New(tiers(provider, fake.New("deep")),
		tool.NewRegistry(editing{}), permission.AllowAll(), agent.WithFinisher(finisher))

	outcome, err := loop.Run(context.Background(), newThread(t), basicPrefix(),
		"Edit a.go", router.Input{Override: router.ChoiceBalanced})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(outcome.FinishFindings) != 1 {
		t.Fatalf("FinishFindings = %v, want the one finding the checks reported", outcome.FinishFindings)
	}

	if outcome.Stop != agent.StopComplete {
		t.Errorf("Stop = %v, want the run to complete: a finding is a bound, not a failure", outcome.Stop)
	}

	if finisher.got.Answer == "" || len(finisher.got.Changes) == 0 {
		t.Errorf("Check got %+v, want the closing answer and the change set", finisher.got)
	}

	for _, req := range provider.Requests() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "named path does not exist") {
				t.Fatal("a finish finding reached the model")
			}
		}
	}
}

// A run that ended on a bound still hands its answer to whoever reads the
// thread, and a run that struggled enough to hit one is the likelier place
// for a name it invented. Two plan runs on a foreign repository ended
// `stagnant` with confident answers that nothing checked.
func TestRun_FinishChecksAlsoBoundARunThatDidNotComplete(t *testing.T) {
	t.Parallel()

	provider := fake.New("deep",
		fake.Turn{
			Text:       []string{"Let me check `internal/agent/toolcall.go` next."},
			StopReason: llm.StopEndTurn,
		},
	)

	finisher := &stubFinisher{}
	loop := agent.New(tiers(fake.New("balanced"), provider),
		tool.NewRegistry(editing{}), permission.AllowAll(), agent.WithFinisher(finisher))

	outcome, err := loop.Run(context.Background(), newThread(t), basicPrefix(),
		"Look at a.go", router.Input{Override: router.ChoiceDeep})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if outcome.Stop == agent.StopComplete {
		t.Fatalf("Stop = %v, want the run bounded rather than completed", outcome.Stop)
	}

	if finisher.got.Answer == "" {
		t.Fatalf("Check got %+v, want the closing prose of the bounded run", finisher.got)
	}

	if len(outcome.FinishFindings) != 1 {
		t.Errorf("FinishFindings = %v, want the finding the checks reported", outcome.FinishFindings)
	}
}
