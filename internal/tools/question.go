package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kyleking/wavez/internal/tool"
)

var questionSchema = buildSchema(map[string]schemaProperty{
	propQuestion: {
		Type: schemaTypeString,
		Description: "A short, specific question for the user. Ask only when the answer " +
			"changes what you do next; do not ask for permission to do something the " +
			"safety gate already covers.",
	},
}, propQuestion)

// Asker answers a question posed to the user. A headless run and the TUI
// each implement it differently: one may return a fixed default or an
// error, the other prompts and blocks for input.
type Asker interface {
	Ask(ctx context.Context, question string) (string, error)
}

// Question asks the user something through an injected Asker, so the tool
// itself carries no assumption about headless or interactive use.
type Question struct {
	asker Asker
}

// NewQuestion builds a Question tool backed by asker.
func NewQuestion(asker Asker) *Question {
	return &Question{asker: asker}
}

// Name implements tool.Tool.
func (*Question) Name() string { return propQuestion }

// Description implements tool.Tool.
func (*Question) Description() string {
	return "Ask the user a short question and wait for their answer. Only the text of the " +
		"question is shown, so state it completely; the user cannot see your reasoning."
}

// Schema implements tool.Tool.
func (*Question) Schema() json.RawMessage { return questionSchema }

// Risk implements tool.Tool. Asking stops for a human answer.
func (*Question) Risk() tool.RiskClass { return tool.RiskExternal }

type questionInput struct {
	Question string `json:"question"`
}

// Run implements tool.Tool.
func (q *Question) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("question: %w", err)
	}

	var in questionInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if in.Question == "" {
		return tool.Fail(tool.CauseBadInput, "question is required"), nil
	}

	answer, err := q.asker.Ask(ctx, in.Question)
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "could not get an answer: %v", err), nil
	}

	return tool.Result{Content: answer}, nil
}
