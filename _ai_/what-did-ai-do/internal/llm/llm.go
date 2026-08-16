// Package llm gets structured judgments from the user's local Claude Code
// CLI installation rather than the Anthropic HTTP API. Shelling out to
// `claude` lets callers reuse the user's existing login (OAuth/keychain)
// instead of requiring a separate ANTHROPIC_API_KEY and billing setup.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// errClaudeCLI wraps errors returned by the claude CLI itself (e.g. "not
// logged in"), as opposed to errors in invoking or parsing its output.
var errClaudeCLI = errors.New("claude CLI returned an error")

// errNoJudgmentsArray is returned when a claude CLI result parses as JSON
// but contains no array-valued field usable as []Judgment.
var errNoJudgmentsArray = errors.New("claude CLI result did not contain a judgments array")

// defaultModel is the cheapest Claude Code model, used unless the caller
// overrides Client.Model. Batching a whole session's decisions into one
// call keeps the fixed system-prompt/context cost (observed ~$0.02/call)
// from being paid once per decision.
const defaultModel = "claude-haiku-4-5"

// Judgment is one decision's adversarial verdict, returned by the model.
type Judgment struct {
	DecisionID string  `json:"decision_id"`
	Assessment string  `json:"assessment"`
	Category   string  `json:"category"`
	Concern    string  `json:"concern"`
	Suggestion string  `json:"suggestion,omitempty"`
	Confidence float64 `json:"confidence"`
}

// runFunc runs the claude CLI with the given args and stdin, returning its
// stdout. It is a seam so tests can avoid spawning a real subprocess.
type runFunc func(ctx context.Context, args []string, stdin string) (stdout string, err error)

// Client talks to the local claude CLI to produce batched Judgments.
type Client struct {
	run   runFunc
	Model string
}

// NewClient returns a Client that shells out to the real claude CLI.
func NewClient() *Client {
	return &Client{
		Model: defaultModel,
		run:   runCLI,
	}
}

// resultEnvelope is the top-level shape of `claude -p --output-format json`.
type resultEnvelope struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// Judge sends systemPrompt and userPrompt to the local claude CLI,
// requesting output conforming to jsonSchema (a JSON Schema describing a
// JSON array of Judgment objects), and returns the parsed []Judgment.
func (c *Client) Judge(
	ctx context.Context,
	systemPrompt, userPrompt, jsonSchema string,
) ([]Judgment, error) {
	model := c.Model
	if model == "" {
		model = defaultModel
	}

	args := []string{
		"-p",
		"--model", model,
		"--output-format", "json",
		"--json-schema", jsonSchema,
		"--append-system-prompt", systemPrompt,
		"--dangerously-skip-permissions",
	}

	stdout, err := c.run(ctx, args, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("running claude CLI: %w", err)
	}

	var envelope resultEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		return nil, fmt.Errorf("parsing claude CLI output envelope: %w", err)
	}

	if envelope.IsError {
		return nil, fmt.Errorf("%w: %s", errClaudeCLI, envelope.Result)
	}

	payload := stripCodeFence(envelope.Result)

	return parseJudgments(payload)
}

// parseJudgments accepts either a bare JSON array of Judgment or a single
// top-level JSON object with one array-valued field containing them.
// The latter exists because Anthropic's tool-calling API requires a
// --json-schema's root type to be "object", not "array" — so a caller
// asking for structured array output typically gets it wrapped one level.
func parseJudgments(payload string) ([]Judgment, error) {
	var judgments []Judgment
	if err := json.Unmarshal([]byte(payload), &judgments); err == nil {
		return judgments, nil
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &wrapper); err != nil {
		return nil, fmt.Errorf("parsing judgments from claude CLI result: %w", err)
	}

	for _, raw := range wrapper {
		if err := json.Unmarshal(raw, &judgments); err == nil {
			return judgments, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", errNoJudgmentsArray, payload)
}

// stripCodeFence removes a ```json ... ``` (or bare ```...```) markdown
// fence if present. --json-schema biases the model toward clean JSON but
// doesn't guarantee it, so this defensive step runs unconditionally.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}

	s = strings.TrimPrefix(s, "```")
	if nl := strings.IndexByte(s, '\n'); nl != -1 {
		firstLine := strings.TrimSpace(s[:nl])
		if firstLine == "" || firstLine == "json" {
			s = s[nl+1:]
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")

	return strings.TrimSpace(s)
}
