package hook

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// SessionEnv names the variable that tells `wavez check` which session it
// runs in. SessionStart sets it through the harness's environment file.
const SessionEnv = "WAVEZ_SESSION"

// EnvFileEnv names the variable Claude Code sets for a SessionStart hook to
// the file whose export lines later shell commands of the session see.
const EnvFileEnv = "CLAUDE_ENV_FILE"

const envFilePerm = 0o600

// appendSessionExport appends the line that sets SessionEnv to id to the
// harness's environment file. Without a file there is nothing to do. The
// id is single-quoted, so no character in it reaches the shell as syntax.
func appendSessionExport(envFile, id string) error {
	if envFile == "" || id == "" {
		return nil
	}

	line := fmt.Sprintf("export %s='%s'\n", SessionEnv, strings.ReplaceAll(id, "'", `'\''`))

	const flags = os.O_APPEND | os.O_CREATE | os.O_WRONLY

	file, err := os.OpenFile(envFile, flags, envFilePerm) //nolint:gosec // the harness names it
	if err != nil {
		return fmt.Errorf("opening %s: %w", EnvFileEnv, err)
	}

	_, err = file.WriteString(line)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("writing %s: %w", EnvFileEnv, err)
	}

	return nil
}
