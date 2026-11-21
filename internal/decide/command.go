package decide

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// PromptPlaceholder is the argument NewCommand replaces with the prompt.
const PromptPlaceholder = "{prompt}"

// DefaultCommandTimeout bounds one run of a Command.
const DefaultCommandTimeout = 2 * time.Minute

// commandWaitDelay is how long a killed command's pipes are waited on.
const commandWaitDelay = time.Second

// ErrNoCommand is returned by a Command built with no argv.
var ErrNoCommand = errors.New("no command")

// Command is a Backend that runs a program once per question and reads the
// option from its standard output. It exists to evaluate a corpus against a
// hosted model. A process per answer is too slow for a hook.
type Command struct {
	name    string
	model   string
	argv    []string
	timeout time.Duration
}

// CommandOption configures a Command.
type CommandOption func(*Command)

// WithCommandTimeout replaces DefaultCommandTimeout. Zero or less leaves
// only the caller's context to stop the program.
func WithCommandTimeout(d time.Duration) CommandOption {
	return func(c *Command) { c.timeout = d }
}

// NewCommand returns a backend that runs argv. Every argument equal to
// PromptPlaceholder is replaced by the prompt, and an argv without one is
// given the prompt on standard input. The name is what Decision.Backend
// reports and model is what Decision.Model reports; neither is passed to
// the program unless argv says so.
func NewCommand(name, model string, argv []string, opts ...CommandOption) *Command {
	c := &Command{name: name, model: model, argv: slices.Clone(argv), timeout: DefaultCommandTimeout}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// NewClaudeCommand returns the Command that asks Claude Code headless:
// claude -p <prompt> --model <model> --output-format text. Each answer is a
// billed request, so it is for a corpus run a person starts.
func NewClaudeCommand(model string, opts ...CommandOption) *Command {
	argv := []string{"claude", "-p", PromptPlaceholder, "--model", model, "--output-format", "text"}

	return NewCommand("claude", model, argv, opts...)
}

// Name is the value Decision.Backend takes for an answer from this backend.
func (c *Command) Name() string { return c.name }

// Model is the model the command is said to ask.
func (c *Command) Model() string { return c.model }

// Answer runs the command and maps the first token it prints to one of q's
// keys. A command that cannot start, exits non-zero, or prints no option is
// an error.
func (c *Command) Answer(ctx context.Context, q Question, state string) (Answer, error) {
	if len(c.argv) == 0 {
		return Answer{}, ErrNoCommand
	}

	user, err := userPrompt(q, state)
	if err != nil {
		return Answer{}, err
	}

	prompt := systemPrompt + "\n\n" + user

	if c.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	args := slices.Clone(c.argv[1:])
	onStdin := true

	for i, arg := range args {
		if arg == PromptPlaceholder {
			args[i], onStdin = prompt, false
		}
	}

	//nolint:gosec // running the argv the caller configured is this backend's purpose
	cmd := exec.CommandContext(ctx, c.argv[0], args...)
	cmd.WaitDelay = commandWaitDelay

	if onStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}

	var stdout bytes.Buffer

	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return Answer{}, fmt.Errorf("running %s: %w", c.argv[0], err)
	}

	key, err := parseReply(q, stdout.String())
	if err != nil {
		return Answer{}, err
	}

	return Answer{Key: key, Model: c.model}, nil
}
