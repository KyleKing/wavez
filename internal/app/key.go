package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrEmptyKeyCommand reports a key command that produced no output, which
// means an unlocked secret store was expected and was not there.
var ErrEmptyKeyCommand = errors.New("hosted key command produced no output")

// hostedKey resolves the hosted API key from the configured command, falling
// back to the environment. The command's stdout is the key and is never
// logged; a git credential helper works the same way, so the secret stays out
// of the environment, the repo, and the process table.
func hostedKey(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return os.Getenv(HostedAPIKeyEnv), nil
	}

	fields := strings.Fields(command)

	//nolint:gosec // running the user's own configured command is the point
	out, err := exec.CommandContext(ctx, fields[0], fields[1:]...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("hosted key command %q failed: %w: %s",
				fields[0], err, strings.TrimSpace(string(exitErr.Stderr)))
		}

		return "", fmt.Errorf("hosted key command %q failed: %w", fields[0], err)
	}

	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", fmt.Errorf("%w: %q", ErrEmptyKeyCommand, fields[0])
	}

	return key, nil
}
