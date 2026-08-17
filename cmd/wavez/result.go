package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/thread"
)

// runResult is one headless run, as a machine-readable record. The
// benchmark harness compares wavez against other agents on exactly these
// numbers, so every field it scores on is here rather than parsed back out
// of the human line.
type runResult struct {
	Thread          string   `json:"thread"`
	Stop            string   `json:"stop"`
	Text            string   `json:"text"`
	Checkpoint      string   `json:"checkpoint,omitempty"`
	Review          string   `json:"review,omitempty"`
	ReviewNote      string   `json:"review_note,omitempty"`
	Strayed         []string `json:"strayed,omitempty"`
	ElapsedSeconds  float64  `json:"elapsed_seconds"`
	Turns           int      `json:"turns"`
	ToolCalls       int      `json:"tool_calls"`
	InputTokens     int      `json:"input_tokens"`
	OutputTokens    int      `json:"output_tokens"`
	TokensCompacted int      `json:"tokens_compacted"`
	HostedSpendUSD  float64  `json:"hosted_spend_usd"`
	Complete        bool     `json:"complete"`
}

func newRunResult(id thread.ID, text string, outcome agent.Outcome, strayed []string) runResult {
	return runResult{
		Thread:          string(id),
		Stop:            string(outcome.Stop),
		Complete:        outcome.Stop == agent.StopComplete,
		Text:            text,
		Checkpoint:      outcome.Checkpoint,
		Review:          string(outcome.Review.Result),
		ReviewNote:      outcome.Review.Note,
		Strayed:         strayed,
		ElapsedSeconds:  outcome.Elapsed.Round(time.Millisecond).Seconds(),
		Turns:           outcome.Turns,
		ToolCalls:       outcome.ToolCalls,
		InputTokens:     outcome.InputTokens,
		OutputTokens:    outcome.OutputTokens,
		TokensCompacted: outcome.TokensCompacted,
		HostedSpendUSD:  outcome.HostedSpendUSD,
	}
}

// writeJSON prints the record as one indented object on stdout, so a run
// with -json emits nothing else there and a caller can pipe it straight
// into jq.
func writeJSON(w io.Writer, res runResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("encoding run result: %w", err)
	}

	return nil
}
