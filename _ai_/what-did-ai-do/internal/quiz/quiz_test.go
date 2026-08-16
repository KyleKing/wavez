package quiz_test

import (
	"testing"

	"github.com/kyleking/what-did-ai-do/internal/decision"
	"github.com/kyleking/what-did-ai-do/internal/quiz"
)

func TestGenerate_RecallQuestionForEveryToolDecision(t *testing.T) {
	t.Parallel()

	decisions := []decision.Decision{
		{ID: "d1", Summary: "edited foo.go", ToolNames: []string{"Edit"}},
		{ID: "d2", Summary: "ran go test", ToolNames: []string{"Bash"}},
	}

	got := quiz.Generate(decisions)

	var recall int

	for _, q := range got {
		if q.Type == quiz.TypeRecall {
			recall++

			if len(q.Choices) < 2 {
				t.Errorf("question %s has %d choices, want >= 2", q.ID, len(q.Choices))
			}

			if q.Choices[q.AnswerIndex] == "" {
				t.Errorf(
					"question %s AnswerIndex %d out of range choices %v",
					q.ID,
					q.AnswerIndex,
					q.Choices,
				)
			}
		}
	}

	if recall != 2 {
		t.Errorf("recall questions = %d, want 2", recall)
	}
}

func TestGenerate_RationaleQuestionOnlyWhenTwoOrMoreRationalesExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		decisions     []decision.Decision
		wantRationale int
	}{
		{
			name: "single decision with rationale has no distractor, no rationale question",
			decisions: []decision.Decision{
				{ID: "d1", Summary: "a", ToolNames: []string{"Edit"}, Rationale: "because X"},
			},
			wantRationale: 0,
		},
		{
			name: "two decisions with rationale, each becomes a distractor for the other",
			decisions: []decision.Decision{
				{ID: "d1", Summary: "a", ToolNames: []string{"Edit"}, Rationale: "because X"},
				{ID: "d2", Summary: "b", ToolNames: []string{"Bash"}, Rationale: "because Y"},
			},
			wantRationale: 2,
		},
		{
			name: "decision with no rationale never gets a rationale question",
			decisions: []decision.Decision{
				{ID: "d1", Summary: "a", ToolNames: []string{"Edit"}},
			},
			wantRationale: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := quiz.Generate(tt.decisions)

			var rationale int

			for _, q := range got {
				if q.Type == quiz.TypeRationale {
					rationale++
				}
			}

			if rationale != tt.wantRationale {
				t.Errorf("rationale questions = %d, want %d", rationale, tt.wantRationale)
			}
		})
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	t.Parallel()

	decisions := []decision.Decision{
		{ID: "d1", Summary: "a", ToolNames: []string{"Edit"}, Rationale: "because X"},
		{ID: "d2", Summary: "b", ToolNames: []string{"Bash"}, Rationale: "because Y"},
		{ID: "d3", Summary: "c", ToolNames: []string{"Read"}, Rationale: "because Z"},
	}

	first := quiz.Generate(decisions)
	second := quiz.Generate(decisions)

	if len(first) != len(second) {
		t.Fatalf("len mismatch: %d vs %d", len(first), len(second))
	}

	for i := range first {
		if first[i].AnswerIndex != second[i].AnswerIndex {
			t.Errorf(
				"question %d AnswerIndex not stable: %d vs %d",
				i,
				first[i].AnswerIndex,
				second[i].AnswerIndex,
			)
		}

		if first[i].Choices[first[i].AnswerIndex] != second[i].Choices[second[i].AnswerIndex] {
			t.Errorf("question %d correct choice not stable across calls", i)
		}
	}
}

func TestGenerate_EmptyDecisions(t *testing.T) {
	t.Parallel()

	got := quiz.Generate(nil)
	if len(got) != 0 {
		t.Errorf("Generate(nil) = %v, want empty", got)
	}
}

func TestGenerate_NoToolNamesSkipsRecall(t *testing.T) {
	t.Parallel()

	decisions := []decision.Decision{
		{ID: "d1", Summary: "mystery"},
	}

	got := quiz.Generate(decisions)
	if len(got) != 0 {
		t.Errorf("Generate() = %v, want empty (no ToolNames, no Rationale)", got)
	}
}
