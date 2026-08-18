package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/tool"
)

var hypothesisSchema = buildSchema(map[string]schemaProperty{
	"cause": {
		Type:        schemaTypeString,
		Description: "The candidate cause you tested, in one line.",
	},
	"experiment": {
		Type:        schemaTypeString,
		Description: "What you did to test it: the command you ran, the file you read, the edit you made.",
	},
	"observation": {
		Type:        schemaTypeString,
		Description: "What that showed, quoting the line or number rather than summarizing it.",
	},
	"verdict": {
		Type:        schemaTypeString,
		Description: "Whether the experiment confirmed the cause, falsified it, or left it open.",
		Enum:        []string{"confirmed", "falsified", "open"},
	},
}, "cause", "experiment", "observation", "verdict")

// HypothesisRecorder collects the ledger rows a phase carries forward.
// *cycle.Ledger satisfies it.
type HypothesisRecorder interface {
	RecordHypothesis(h cycle.Hypothesis) error
}

// Hypothesis records one row of the ledger a Cycle phase carries to the
// next: the candidate cause, the experiment, the observation, and the
// verdict. The rows are what crosses a phase boundary in place of the
// transcript, and no exit Condition reads them.
type Hypothesis struct {
	recorder HypothesisRecorder
}

// NewHypothesis builds a Hypothesis tool backed by recorder.
func NewHypothesis(recorder HypothesisRecorder) *Hypothesis {
	return &Hypothesis{recorder: recorder}
}

// Name implements tool.Tool.
func (*Hypothesis) Name() string { return "hypothesis" }

// Description implements tool.Tool.
func (*Hypothesis) Description() string {
	return "Record one candidate cause you tested and what the experiment showed. Record the " +
		"causes you falsified too: they are what stops the next phase repeating your work. " +
		"This is a note, not a claim of progress, and nothing you record here decides whether " +
		"the phase advances."
}

// Schema implements tool.Tool.
func (*Hypothesis) Schema() json.RawMessage { return hypothesisSchema }

type hypothesisInput struct {
	Cause       string `json:"cause"`
	Experiment  string `json:"experiment"`
	Observation string `json:"observation"`
	Verdict     string `json:"verdict"`
}

// Run implements tool.Tool.
func (h *Hypothesis) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("hypothesis: %w", err)
	}

	var in hypothesisInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	if in.Cause == "" || in.Experiment == "" || in.Observation == "" {
		return tool.Errorf("cause, experiment, and observation are all required"), nil
	}

	row := cycle.Hypothesis{
		Cause:       in.Cause,
		Experiment:  in.Experiment,
		Observation: in.Observation,
		Verdict:     in.Verdict,
	}
	if err := h.recorder.RecordHypothesis(row); err != nil {
		return tool.Errorf("could not record the hypothesis: %v", err), nil
	}

	return tool.Result{Content: "recorded: " + in.Cause + " (" + in.Verdict + ")"}, nil
}
