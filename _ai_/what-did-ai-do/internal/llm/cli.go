package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// errClaudeNotFound is returned when the claude binary isn't on PATH.
var errClaudeNotFound = errors.New(
	"claude CLI not found on PATH: install Claude Code to use adversarial analysis",
)

// runCLI is the real runFunc implementation, invoking the local claude
// binary. --bare is deliberately not used: it strictly requires
// ANTHROPIC_API_KEY/apiKeyHelper and bypasses the OAuth/keychain login,
// defeating the point of using the CLI the user is already logged into.
func runCLI(ctx context.Context, args []string, stdin string) (string, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "", errClaudeNotFound
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = bytes.NewBufferString(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && stdout.Len() > 0 {
			// claude -p exits non-zero on API errors (e.g. a bad
			// --json-schema) but still writes a JSON envelope with
			// is_error/result describing what went wrong. Returning it lets
			// Judge surface that real message instead of a bare exit code.
			return stdout.String(), nil
		}

		if errors.As(err, &exitErr) {
			return "", fmt.Errorf(
				"claude CLI exited with error: %w (stderr: %s)",
				err,
				stderr.String(),
			)
		}

		return "", fmt.Errorf("executing claude CLI: %w", err)
	}

	return stdout.String(), nil
}
