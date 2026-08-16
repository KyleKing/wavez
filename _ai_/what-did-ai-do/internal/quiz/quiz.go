// Package quiz turns extracted Decisions into multiple-choice questions:
// recall questions test structural facts (which tool touched which file),
// rationale questions test whether the user understood why.
package quiz

import (
	"fmt"
	"hash/fnv"

	"github.com/kyleking/what-did-ai-do/internal/decision"
)

// Type distinguishes what a Question is testing.
type Type string

const (
	// TypeRecall asks about a structural fact (which tool, which file).
	TypeRecall Type = "recall"
	// TypeRationale asks why the agent made a decision; only generated
	// when a Decision has a non-empty Rationale.
	TypeRationale Type = "rationale"
)

// Question is one multiple-choice question generated from a Decision.
type Question struct {
	ID          string
	DecisionID  string
	Type        Type
	Prompt      string
	Choices     []string
	AnswerIndex int
}

// fallbackTools backfills recall-question distractors when a batch doesn't
// contain enough other decisions to draw real ones from.
var fallbackTools = []string{"Edit", "Write", "Bash", "Read", "Grep", "WebSearch"}

// minChoices is the fewest options a multiple-choice question can have and
// still be meaningful (the correct answer plus at least one distractor).
const minChoices = 2

// Generate builds recall questions for every decision (using other
// decisions' tools as distractors) and rationale questions for decisions
// whose Rationale is non-empty and for which at least one other rationale
// exists to serve as a distractor.
func Generate(decisions []decision.Decision) []Question {
	toolPool := distinctTools(decisions)
	rationalePool := distinctRationales(decisions)

	questions := make([]Question, 0, len(decisions))

	for i := range decisions {
		d := &decisions[i]

		if q, ok := recallQuestion(d, toolPool); ok {
			questions = append(questions, q)
		}

		if q, ok := rationaleQuestion(d, rationalePool); ok {
			questions = append(questions, q)
		}
	}

	return questions
}

func recallQuestion(d *decision.Decision, toolPool []string) (Question, bool) {
	if len(d.ToolNames) == 0 {
		return Question{}, false
	}

	correct := d.ToolNames[0]
	choices := distractors(correct, toolPool, fallbackTools)

	if len(choices) < minChoices {
		return Question{}, false
	}

	return Question{
		ID:          d.ID + "-recall",
		DecisionID:  d.ID,
		Type:        TypeRecall,
		Prompt:      fmt.Sprintf("Which tool did the agent use for: %s?", d.Summary),
		Choices:     choices,
		AnswerIndex: placeAnswer(d.ID+"-recall", choices),
	}, true
}

func rationaleQuestion(d *decision.Decision, rationalePool []string) (Question, bool) {
	if d.Rationale == "" {
		return Question{}, false
	}

	choices := distractors(d.Rationale, rationalePool, nil)
	if len(choices) < minChoices {
		return Question{}, false
	}

	return Question{
		ID:          d.ID + "-rationale",
		DecisionID:  d.ID,
		Type:        TypeRationale,
		Prompt:      fmt.Sprintf("Why did the agent do this: %s?", d.Summary),
		Choices:     choices,
		AnswerIndex: placeAnswer(d.ID+"-rationale", choices),
	}, true
}

// distractors returns up to 3 wrong choices for correct, drawn first from
// pool (deduplicated, excluding correct), then padded from fallback if pool
// runs short.
func distractors(correct string, pool, fallback []string) []string {
	choices := []string{correct}
	seen := map[string]bool{correct: true}

	for _, candidates := range [][]string{pool, fallback} {
		for _, c := range candidates {
			if len(choices) >= 4 || seen[c] {
				continue
			}

			seen[c] = true

			choices = append(choices, c)
		}
	}

	return choices
}

func distinctTools(decisions []decision.Decision) []string {
	seen := map[string]bool{}

	var out []string

	for i := range decisions {
		for _, name := range decisions[i].ToolNames {
			if !seen[name] {
				seen[name] = true

				out = append(out, name)
			}
		}
	}

	return out
}

func distinctRationales(decisions []decision.Decision) []string {
	seen := map[string]bool{}

	var out []string

	for i := range decisions {
		if decisions[i].Rationale != "" && !seen[decisions[i].Rationale] {
			seen[decisions[i].Rationale] = true

			out = append(out, decisions[i].Rationale)
		}
	}

	return out
}

// placeAnswer deterministically moves the correct choice (at index 0, per
// distractors) to a seed-derived position, so repeated Generate calls over
// the same decisions produce the same layout without needing a random
// source. Choices is deduplicated by distractors, so the swap is unambiguous.
func placeAnswer(seed string, choices []string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	target := int(h.Sum32()) % len(choices)

	choices[0], choices[target] = choices[target], choices[0]

	return target
}
