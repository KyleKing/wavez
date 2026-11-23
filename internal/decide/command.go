package decide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// How long one headless run may take.
	claudeTimeout = 2 * time.Minute
	// How long a killed run's pipes are waited on.
	claudeWaitDelay = time.Second
)

// ErrClaude is returned when a headless run reports an error of its own.
var ErrClaude = errors.New("claude reported an error")

// Claude is a Backend that asks Claude Code headless, one process per
// question. It exists for one use: `wavez route eval --claude <model>`,
// which measures a hosted model against a corpus. Every answer is a billed
// request, so nothing starts it but a person.
type Claude struct {
	model string
}

// NewClaude returns the backend that runs `claude -p --model <model>`.
func NewClaude(model string) *Claude {
	return &Claude{model: model}
}

// Name is the value Decision.Backend takes for an answer from this backend.
func (*Claude) Name() string { return "claude" }

// claudeArgs is the headless invocation. The classifier's instructions
// replace the default system prompt, and the run loads nothing of the
// user's or the project's: no settings, hooks, CLAUDE.md, skills, plugins,
// MCP servers, or tools, and it leaves no session behind.
func claudeArgs(model string) []string {
	return []string{
		"-p",
		"--model", model,
		"--output-format", "json",
		"--system-prompt", systemPrompt,
		"--tools", "",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--safe-mode",
		"--disable-slash-commands",
		"--no-session-persistence",
	}
}

type claudeResult struct {
	Result string `json:"result"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	IsError bool `json:"is_error"`
}

// Answer runs claude with the question on standard input, from the
// temporary directory so no project is in reach, and maps its result to
// one of q's keys. A run that cannot start, exits non-zero, reports an
// error, or answers with anything but one option is an error.
func (c *Claude) Answer(ctx context.Context, q Question, state string) (Answer, error) {
	prompt, err := userPrompt(q, state)
	if err != nil {
		return Answer{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()

	//nolint:gosec // the one variable argument is the model name the person running the evaluation gave
	cmd := exec.CommandContext(ctx, "claude", claudeArgs(c.model)...)
	cmd.WaitDelay = claudeWaitDelay
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.Output()
	if err != nil {
		return Answer{}, fmt.Errorf("running claude: %w", err)
	}

	var result claudeResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		return Answer{}, fmt.Errorf("reading claude's output: %w", err)
	}

	if result.IsError {
		return Answer{}, fmt.Errorf("%w: %s", ErrClaude, truncate(result.Result, replyExcerpt))
	}

	key, err := parseReply(q, result.Result)
	if err != nil {
		return Answer{}, err
	}

	return Answer{
		Key:          key,
		Model:        c.model,
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
	}, nil
}
