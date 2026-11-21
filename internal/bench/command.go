package bench

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Command runs any program as a harness: its stdout is the answer and the
// wall clock is the only cost known. It lets another harness join a
// comparison before anyone writes a parser for it.
type Command struct {
	argv []string
}

// Placeholders an argv template may carry.
const (
	placeholderPrompt = "{prompt}"
	placeholderModel  = "{model}"
	placeholderDir    = "{dir}"
)

// ErrEmptyTemplate is returned for a template with no program in it.
var ErrEmptyTemplate = errors.New("harness command is empty")

// NewCommand reads an argv template such as `pi -p {prompt}`. The template
// is split into arguments once, on whitespace outside quotes, and only then
// are the placeholders filled, so a prompt is always one argument however
// many spaces and quotes it holds.
func NewCommand(template string) (*Command, error) {
	argv := splitArgs(template)
	if len(argv) == 0 {
		return nil, ErrEmptyTemplate
	}

	return &Command{argv: argv}, nil
}

// Run executes the template with the request filled in.
func (c *Command) Run(ctx context.Context, req Request) (Outcome, error) {
	fill := strings.NewReplacer(
		placeholderPrompt, req.Prompt, placeholderModel, req.Model, placeholderDir, req.Dir,
	)

	args := make([]string, 0, len(c.argv))
	for _, arg := range c.argv {
		args = append(args, fill.Replace(arg))
	}

	start := time.Now()
	stdout, err := output(ctx, invocation{Dir: req.Dir, Name: args[0], Args: args[1:], Env: req.Env})
	out := Outcome{Answer: strings.TrimSpace(string(stdout)), Stop: StopCompleted, Duration: time.Since(start)}

	if err != nil {
		if len(stdout) == 0 {
			return Outcome{}, err
		}

		out.Stop = StopError
	}

	return out, nil
}

// splitArgs cuts a template into arguments at unquoted whitespace.
func splitArgs(template string) []string {
	var (
		args  []string
		word  strings.Builder
		quote rune
		open  bool
	)

	for _, r := range template {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote, open = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if open {
				args = append(args, word.String())
				word.Reset()

				open = false
			}
		default:
			word.WriteRune(r)

			open = true
		}
	}

	if open {
		args = append(args, word.String())
	}

	return args
}
