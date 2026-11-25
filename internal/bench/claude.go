package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Claude runs Claude Code headless.
type Claude struct {
	// Bin is the claude binary, looked up on PATH when it has no slash.
	Bin string
	// UserSettings lets the run load the owner's user-level settings. Off,
	// only the workspace's own settings apply, so two arms differ by what
	// was written into the workspace and nothing else.
	UserSettings bool
}

// claudeTools are the tools a run may use without asking. Nobody is there
// to answer a prompt, so anything not named here is denied. The web tools
// are left out so a run cannot depend on the network's mood.
const claudeTools = "Bash,Read,Edit,Write,MultiEdit,NotebookEdit,Glob,Grep,TodoWrite,Task,Agent,Skill"

// args builds the command line. `--permission-mode acceptEdits` with an
// allow list is used instead of `--dangerously-skip-permissions` because
// the harness refuses the latter when run as root.
func (c *Claude) args(req Request) []string {
	args := []string{
		"-p", req.Prompt,
		"--output-format", "json",
		"--permission-mode", "acceptEdits",
		"--allowedTools", claudeTools,
		"--strict-mcp-config",
	}

	if !c.UserSettings {
		args = append(args, "--setting-sources", "project,local")
	}

	if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	return append(args, req.Args...)
}

// claudeResult is the object `claude -p --output-format json` prints.
type claudeResult struct {
	ModelUsage map[string]struct {
		OutputTokens int     `json:"outputTokens"` //nolint:tagliatelle // the harness's name
		CostUSD      float64 `json:"costUSD"`      //nolint:tagliatelle // the harness's name
	} `json:"modelUsage"` //nolint:tagliatelle // the harness's name
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Usage     struct {
		Input         int `json:"input_tokens"`
		Output        int `json:"output_tokens"`
		CacheRead     int `json:"cache_read_input_tokens"`
		CacheCreation int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	NumTurns   int     `json:"num_turns"`
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	IsError    bool    `json:"is_error"`
}

// ResultError says the harness printed something that is not its result
// object.
type ResultError struct {
	// Head is the start of what it printed instead.
	Head string
}

func (e *ResultError) Error() string {
	return "claude printed no result object: " + e.Head
}

// Run executes one headless session and reads the result object it prints.
// A non-zero exit that still printed a result is a run that failed, not a
// run that could not be measured.
func (c *Claude) Run(ctx context.Context, req Request) (Outcome, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}

	stdout, runErr := output(ctx, invocation{Dir: req.Dir, Name: bin, Args: c.args(req), Env: req.Env})

	outcome, err := parseClaude(stdout)
	if err != nil {
		return Outcome{SessionID: req.SessionID}, errors.Join(runErr, err)
	}

	if outcome.SessionID == "" {
		outcome.SessionID = req.SessionID
	}

	return outcome, nil
}

func parseClaude(stdout []byte) (Outcome, error) {
	var res claudeResult

	if err := json.Unmarshal(bytes.TrimSpace(stdout), &res); err != nil || res.Type != "result" {
		return Outcome{}, &ResultError{Head: firstLine(string(stdout))}
	}

	out := Outcome{
		Answer:    res.Result,
		SessionID: res.SessionID,
		Stop:      StopCompleted,
		Turns:     res.NumTurns,
		CostUSD:   res.CostUSD,
		Duration:  time.Duration(res.DurationMS) * time.Millisecond,
		Usage: Usage{
			Input: res.Usage.Input, Output: res.Usage.Output,
			CacheRead: res.Usage.CacheRead, CacheCreation: res.Usage.CacheCreation,
		},
	}

	if res.IsError || res.Subtype != "success" {
		out.Stop = failedStop(res.Subtype)
	}

	out.Served = servedModel(&res)

	return out, nil
}

// failedStop names a failed run by the harness's subtype, which is the only
// place it says whether it ran out of turns or broke.
func failedStop(subtype string) string {
	if subtype == "" || subtype == "success" {
		return StopError
	}

	return subtype
}

// servedModel is the model that did the work. The harness also bills small
// side requests to a cheaper model, so the answer is the one that cost the
// most, with output tokens and then the name breaking ties.
func servedModel(res *claudeResult) string {
	names := make([]string, 0, len(res.ModelUsage))
	for name := range res.ModelUsage {
		names = append(names, name)
	}

	if len(names) == 0 {
		return ""
	}

	slices.SortFunc(names, func(a, b string) int {
		ua, ub := res.ModelUsage[a], res.ModelUsage[b]

		switch {
		case ua.CostUSD != ub.CostUSD:
			return compareDesc(ua.CostUSD, ub.CostUSD)
		case ua.OutputTokens != ub.OutputTokens:
			return compareDesc(float64(ua.OutputTokens), float64(ub.OutputTokens))
		default:
			return strings.Compare(a, b)
		}
	})

	return names[0]
}

func compareDesc(a, b float64) int {
	if a > b {
		return -1
	}

	return 1
}
