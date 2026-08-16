package adversarial_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/kyleking/what-did-ai-do/internal/adversarial"
	"github.com/kyleking/what-did-ai-do/internal/llm"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// TestGoldenExamples_RegressionAgainstRealCLI is an opt-in regression check:
// it re-judges the same examples embedded as few-shot calibration in the
// system prompt (see examples.json) against the REAL local claude CLI, and
// checks the live prompt still classifies them as expected. Real LLM output
// varies run to run and costs real money/requires a login, so this is
// skipped by default; run with WDAI_LLM_TESTS=1 when tuning the rubric or
// growing examples.json, to catch drift before it ships.
func TestGoldenExamples_RegressionAgainstRealCLI(t *testing.T) {
	t.Parallel()

	if os.Getenv("WDAI_LLM_TESTS") != "1" {
		t.Skip(
			"set WDAI_LLM_TESTS=1 to run this against the real claude CLI (costs real money, requires login)",
		)
	}

	type example struct {
		ID                 string `json:"id"`
		Summary            string `json:"summary"`
		Rationale          string `json:"rationale"`
		Diff               string `json:"diff"`
		ExpectedAssessment string `json:"expected_assessment"`
	}

	raw, err := os.ReadFile("examples.json")
	if err != nil {
		t.Fatalf("ReadFile(examples.json) error = %v", err)
	}

	var examples []example
	if err := json.Unmarshal(raw, &examples); err != nil {
		t.Fatalf("Unmarshal(examples.json) error = %v", err)
	}

	dir := t.TempDir()
	client := llm.NewClient()

	for _, ex := range examples {
		t.Run(ex.ID, func(t *testing.T) {
			t.Parallel()

			s := session.Session{
				ID:          "golden-" + ex.ID,
				Agent:       session.AgentClaudeCode,
				ProjectPath: dir,
				Messages: []session.Message{
					{Role: "assistant", Text: ex.Rationale},
				},
				ToolCalls: []session.ToolCall{
					{Name: "Bash", Input: `{"command":` + strconv.Quote(ex.Diff) + `}`},
				},
			}

			report, err := adversarial.New(client).Analyze(context.Background(), &s)
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}

			got := "sound"
			if len(report.Findings) > 0 {
				got = report.Findings[0].Judgment.Assessment
			}

			if got != ex.ExpectedAssessment {
				t.Errorf("assessment for %q = %q, want %q", ex.ID, got, ex.ExpectedAssessment)
			}
		})
	}
}
