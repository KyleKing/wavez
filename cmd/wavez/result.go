package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/cycle"
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
	ThreadSpendUSD  float64  `json:"thread_spend_usd"`
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
		ThreadSpendUSD:  outcome.ThreadSpendUSD,
	}
}

// cycleResult is one headless cycle, as a machine-readable record. Stop and
// the phase rows are what a reader has to see: a cycle that ran every phase
// and one the harness refused to advance differ only there.
type cycleResult struct {
	Cycle          string        `json:"cycle"`
	Stop           string        `json:"stop"`
	Phase          string        `json:"phase"`
	Condition      string        `json:"condition"`
	Reason         string        `json:"reason"`
	Phases         []phaseResult `json:"phases"`
	Turns          int           `json:"turns"`
	ToolCalls      int           `json:"tool_calls"`
	HostedSpendUSD float64       `json:"hosted_spend_usd"`
	Complete       bool          `json:"complete"`
}

// phaseResult is one phase of a cycleResult.
type phaseResult struct {
	Phase     string `json:"phase"`
	Condition string `json:"condition"`
	Reason    string `json:"reason"`
	Attempts  int    `json:"attempts"`
	Turns     int    `json:"turns"`
	ToolCalls int    `json:"tool_calls"`
	Holds     bool   `json:"holds"`
}

func newCycleResult(outcome cycle.Outcome) cycleResult {
	out := cycleResult{
		Cycle:          outcome.Cycle,
		Stop:           string(outcome.Stop),
		Phase:          outcome.Phase,
		Condition:      outcome.Verdict.Condition,
		Reason:         outcome.Verdict.Reason,
		Complete:       outcome.Stop == cycle.StopComplete,
		Turns:          outcome.Turns,
		ToolCalls:      outcome.ToolCalls,
		HostedSpendUSD: outcome.SpendUSD,
	}

	for _, p := range outcome.Phases {
		out.Phases = append(out.Phases, phaseResult{
			Phase:     p.Phase,
			Condition: p.Verdict.Condition,
			Reason:    p.Verdict.Reason,
			Holds:     p.Verdict.Holds,
			Attempts:  p.Attempts,
			Turns:     p.Turns,
			ToolCalls: p.ToolCalls,
		})
	}

	return out
}

// writeJSON prints the record as one indented object on stdout, so a run
// with -json emits nothing else there and a caller can pipe it straight
// into jq.
func writeJSON(w io.Writer, res any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("encoding run result: %w", err)
	}

	return nil
}
