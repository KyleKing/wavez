package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/kyleking/wavez/internal/lean"
)

// errNoClaude is a run with no Claude Code to start.
var errNoClaude = errors.New("wavez run: claude is not on PATH")

const runUsage = `usage: wavez run [claude arguments...]

Starts Claude Code with a short system prompt in place of its own and the
tools ` + lean.Tools + `. Everything else is passed to claude as it is:

  wavez run                      an interactive session
  wavez run -p "fix the test"    one prompt, headless
  wavez run --model haiku        on a smaller model
  wavez run --continue           the last session in this directory

A --system-prompt or --tools of your own replaces the one wavez would pass.
`

// runRun starts Claude Code lean and waits for it. It is Claude Code's own
// session, on whatever account claude is signed in to, and the hooks, the
// project's instructions, and the connectors load as they would without it.
// The terminal is handed to it whole, so an interactive session works.
func runRun(ctx context.Context, args []string, streams stdio) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, err := fmt.Fprint(streams.out, runUsage)

		return wrapWrite(err)
	}

	bin, err := exec.LookPath("claude")
	if err != nil {
		return errNoClaude
	}

	cmd := exec.CommandContext(ctx, bin, lean.Args(args...)...) //nolint:gosec // claude, with the caller's arguments
	// The streams are the process's own files when a person runs this, and
	// a child is handed a file as it is, so claude sees the terminal.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.in, streams.out, streams.err

	err = cmd.Run()

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exitError(exit.ExitCode())
	}

	if err != nil {
		return fmt.Errorf("wavez run: starting claude: %w", err)
	}

	return nil
}
