package adversarial

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed examples.json
var examplesJSON []byte

// example is one few-shot calibration point, embedded into the system
// prompt on every call and also used by the (opt-in, real-CLI) regression
// test to check the live prompt still classifies them as expected. Grow
// this set over time as flagging quality gets tuned; it's the mechanism
// for "safer to post, but conservative and tuned over time" rather than a
// one-off hand-written rubric.
type example struct {
	ID                    string  `json:"id"`
	Summary               string  `json:"summary"`
	Rationale             string  `json:"rationale"`
	Diff                  string  `json:"diff"`
	FileStateStatus       string  `json:"file_state_status"`
	ExpectedAssessment    string  `json:"expected_assessment"`
	ExpectedCategory      string  `json:"expected_category"`
	Why                   string  `json:"why"`
	ExpectedConfidenceMin float64 `json:"expected_confidence_min"`
}

func loadExamples() ([]example, error) {
	var examples []example
	if err := json.Unmarshal(examplesJSON, &examples); err != nil {
		return nil, fmt.Errorf("parsing embedded examples.json: %w", err)
	}

	return examples, nil
}

// minConfidenceToFlag is the conservative default: only "questionable" or
// "slop" verdicts at or above this confidence are surfaced. Chosen per an
// explicit product decision to favor false negatives over false positives
// — a feature that cries wolf erodes trust faster than one that misses a
// few real issues.
const minConfidenceToFlag = 0.8

const systemPromptTemplate = `You are reviewing decisions an AI coding agent made on a user's behalf, ` +
	`looking specifically for "AI slop": hasty, unjustified, or clearly suboptimal changes that a careful ` +
	`engineer would not have made without a stated reason.

Be conservative. Most AI decisions are fine. Only flag a decision as "slop" or "questionable" when you have ` +
	`a concrete, specific concern you could explain to the engineer who shipped it — never flag something just ` +
	`because it lacks commentary, or because a stylistic alternative exists. A decision with no stated rationale ` +
	`is NOT automatically slop: mechanical actions that plainly follow a direct user instruction need no ` +
	`justification.

Distinguish two categories when you do flag something:
- "own-choice": the agent's own choice looks questionable in hindsight — a grounded, checkable claim (e.g. it ` +
	`skipped a safety check, reached for a destructive fix without diagnosing the problem, or contradicts its ` +
	`own stated rationale).
- "unconsidered-alternative": the change works, but a genuinely better approach existed that neither the human ` +
	`nor the agent considered. This is more speculative — only raise it when you're confident a competent ` +
	`engineer would actually make that different choice, not for minor style preferences.

Assign "assessment" as one of "sound", "questionable", or "slop", and a "confidence" between 0 and 1 for how ` +
	`sure you are. Only decisions you're genuinely confident about (0.8 or higher) will be shown to the user, so ` +
	`do not inflate confidence to get a finding surfaced — an honest low-confidence "questionable" is more ` +
	`useful than a false high-confidence one.

Some flagged decisions include a "file_state" showing whether the change is still present in the codebase today ` +
	`("live"), has been rewritten since ("superseded"), or the file no longer exists ("gone"). Do not flag ` +
	`superseded or gone decisions — the point is moot, they've already been addressed by later work; if you see ` +
	`one, assess it as "sound" with a brief concern noting it's no longer relevant.

Calibration examples (not the decisions to judge — reference points for the standard to apply):

%s

Now judge the following decisions, one judgment object per decision, each with fields: decision_id, ` +
	`assessment, category, confidence, concern, suggestion (suggestion only when category is ` +
	`"unconsidered-alternative", omit or leave empty otherwise).`

func buildSystemPrompt() (string, error) {
	examples, err := loadExamples()
	if err != nil {
		return "", err
	}

	var b strings.Builder

	for i := range examples {
		ex := &examples[i]
		if i > 0 {
			b.WriteString("\n\n")
		}

		fmt.Fprintf(&b, "Example %q:\nSummary: %s\nRationale: %s\nDiff:\n%s\nFile state: %s\n"+
			"Expected: assessment=%s category=%s confidence>=%.2f\nWhy: %s",
			ex.ID, ex.Summary, ex.Rationale, ex.Diff, ex.FileStateStatus,
			ex.ExpectedAssessment, ex.ExpectedCategory, ex.ExpectedConfidenceMin, ex.Why)
	}

	return fmt.Sprintf(systemPromptTemplate, b.String()), nil
}

// judgmentArraySchema is a JSON Schema for []llm.Judgment, passed to the
// local claude CLI's --json-schema flag. The root type must be "object" —
// Anthropic's tool-calling API rejects an array-rooted schema (verified:
// "input_schema.type: Input should be 'object'") — so the array of
// judgments is wrapped in a single field; llm.Judge unwraps it.
const judgmentArraySchema = `{
  "type": "object",
  "properties": {
    "judgments": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "decision_id": {"type": "string"},
          "assessment": {"type": "string", "enum": ["sound", "questionable", "slop"]},
          "category": {"type": "string", "enum": ["own-choice", "unconsidered-alternative", "none"]},
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "concern": {"type": "string"},
          "suggestion": {"type": "string"}
        },
        "required": ["decision_id", "assessment", "category", "confidence", "concern"]
      }
    }
  },
  "required": ["judgments"]
}`

func buildUserPrompt(candidates []Candidate, states map[string]fileStateSummary) string {
	var b strings.Builder

	for i := range candidates {
		c := &candidates[i]
		if i > 0 {
			b.WriteString("\n\n")
		}

		fmt.Fprintf(&b, "Decision %q:\n", c.Decision.ID)
		fmt.Fprintf(&b, "Summary: %s\nRationale: %s\n", c.Decision.Summary, c.Decision.Rationale)

		for j := range c.ToolCalls {
			fmt.Fprintf(&b, "Diff (%s):\n%s\n", c.ToolCalls[j].Name, diffText(&c.ToolCalls[j]))
		}

		if state, ok := states[c.Decision.ID]; ok {
			fmt.Fprintf(&b, "File state: %s\n", state.Status)
		}
	}

	return b.String()
}
