package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// invoke runs one command the way main does and returns its exit status.
func invoke(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := run(t.Context(), args, stdio{in: strings.NewReader(stdin), out: &out, err: &errOut})

	return code, out.String(), errOut.String()
}

func TestInit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")

	code, out, _ := invoke(t, "", "init", "--dir", dir, "--print")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "# .claude/settings.json (created)")
	assert.Contains(t, out, `"command": "wavez hook"`)
	assert.Contains(t, out, "# .claude/skills/wavez-refactor/SKILL.md (created)")
	assert.NoFileExists(t, settings, "--print writes nothing")

	code, out, _ = invoke(t, "", "init", "--dir", dir)
	require.Equal(t, 0, code)
	assert.Equal(t, "created .claude/settings.json\ncreated .claude/skills/wavez-refactor/SKILL.md\n"+
		"created .wavez/.gitignore\n", out)

	code, out, _ = invoke(t, "", "init", "--dir", dir)
	require.Equal(t, 0, code)
	assert.Empty(t, out, "a second run changes nothing and says nothing")

	code, out, _ = invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez")
	require.Equal(t, 0, code)
	assert.Equal(t, "updated .claude/settings.json\n", out)

	raw, err := os.ReadFile(settings) //nolint:gosec // a file this test wrote
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(string(raw), `"/opt/wavez/wavez hook"`))
	assert.NotContains(t, string(raw), `"wavez hook"`)
}

func TestInit_Failures(t *testing.T) {
	t.Parallel()

	t.Run("an unknown flag is a usage error", func(t *testing.T) {
		t.Parallel()

		code, _, errOut := invoke(t, "", "init", "--nope")
		assert.Equal(t, 2, code)
		assert.Contains(t, errOut, "-nope")
	})

	t.Run("--help is not an error", func(t *testing.T) {
		t.Parallel()

		code, _, errOut := invoke(t, "", "init", "--help")
		assert.Equal(t, 0, code)
		assert.Contains(t, errOut, "-bin")
	})

	t.Run("settings that cannot be parsed are left alone", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		settings := filepath.Join(dir, ".claude", "settings.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o750))
		require.NoError(t, os.WriteFile(settings, []byte("{oops"), 0o600))

		code, out, errOut := invoke(t, "", "init", "--dir", dir)
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Contains(t, errOut, "settings.json")

		raw, err := os.ReadFile(settings) //nolint:gosec // a file this test wrote
		require.NoError(t, err)
		assert.Equal(t, "{oops", string(raw))
	})
}

func TestHook_FailureIsOneLineAndNeverStatusTwo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stdin string
	}{
		{name: "empty stdin", stdin: ""},
		{name: "not JSON", stdin: "hello\nworld"},
		{name: "no event name", stdin: `{"cwd": "/tmp"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out, errOut := invoke(t, tt.stdin, "hook")
			assert.Equal(t, 1, code, "status 2 would block the agent; any other failure status does not")
			assert.Empty(t, out)
			assert.True(t, strings.HasPrefix(errOut, "wavez hook: "), errOut)
			assert.Equal(t, 1, strings.Count(errOut, "\n"), "one line: %q", errOut)
		})
	}
}

func TestHook_EventsThatNeedNoGate(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "hook.log")
	t.Setenv(hookLogEnv, logPath)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o600))

	for _, event := range []string{"SessionStart", "Stop", "Notification"} {
		stdin := `{"session_id": "s1", "cwd": "` + root + `", "hook_event_name": "` + event + `"}`

		code, out, errOut := invoke(t, stdin, "hook")
		assert.Equal(t, 0, code, errOut)
		assert.Empty(t, out)
		assert.Empty(t, errOut)
	}

	assert.FileExists(t, filepath.Join(root, ".wavez", "sessions", "s1.json"))

	trace, err := os.ReadFile(logPath) //nolint:gosec // a file this test wrote
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(string(trace), "\n"))
	assert.Contains(t, string(trace), `"event":"Stop"`)
	assert.Contains(t, string(trace), `"note":"nothing changed"`)
}
