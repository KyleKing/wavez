package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/lean"
)

// fakeClaude puts a claude on PATH that prints its arguments, one a line,
// and exits with status.
func fakeClaude(t *testing.T, status string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("NOT TESTED: the stand-in for claude is a shell script")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit " + status + "\n"
	//nolint:gosec // a script, which has to be executable
	require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700))
	t.Setenv("PATH", dir)
}

//nolint:paralleltest // claude is found on PATH, which the test replaces
func TestRun_StartsClaudeLeanAndPassesTheRestThrough(t *testing.T) {
	fakeClaude(t, "0")

	out, err := runCommand(t, runRun, "-p", "fix it", "--model", "haiku")
	require.NoError(t, err)
	assert.Equal(t, "--system-prompt\n"+lean.Prompt+"\n--tools\n"+lean.Tools+"\n-p\nfix it\n--model\nhaiku\n", out)

	out, err = runCommand(t, runRun, "--tools", "Bash")
	require.NoError(t, err)
	assert.Equal(t, "--system-prompt\n"+lean.Prompt+"\n--tools\nBash\n", out)
}

//nolint:paralleltest // claude is found on PATH, which the test replaces
func TestRun_ExitsAsClaudeDid(t *testing.T) {
	fakeClaude(t, "3")

	_, err := runCommand(t, runRun)

	var status exitError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, exitError(3), status)
}

func TestRun_SaysWhenClaudeIsMissingAndPrintsItsUsage(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := runCommand(t, runRun, "-p", "x")
	require.ErrorIs(t, err, errNoClaude)

	out, err := runCommand(t, runRun, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "usage: wavez run")
}
