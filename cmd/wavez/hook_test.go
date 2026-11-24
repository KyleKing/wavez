package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/hook"
)

// invoke runs one command the way main does and returns its exit status.
func invoke(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := run(t.Context(), args, stdio{in: strings.NewReader(stdin), out: &out, err: &errOut})

	return code, out.String(), errOut.String()
}

// event is a hook event's stdin.
func event(t *testing.T, fields map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(fields)
	require.NoError(t, err)

	return string(raw)
}

// goProject is a directory holding a go.mod, which is a project with checks
// to run.
func goProject(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o600))

	return root
}

func TestHook_FailureIsOneLineAndNeverStatusTwo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stdin string
		args  []string
	}{
		{name: "empty stdin", stdin: ""},
		{name: "not JSON", stdin: "hello\nworld"},
		{name: "no event name", stdin: `{"cwd": "/tmp"}`},
		{name: "no cwd", stdin: `{"hook_event_name": "Stop"}`},
		{name: "an argument it does not take", stdin: `{}`, args: []string{"--verbose"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out, errOut := invoke(t, tt.stdin, append([]string{"hook"}, tt.args...)...)
			assert.Equal(t, 1, code, "status 2 would block the agent; any other failure status does not")
			assert.Empty(t, out)
			assert.True(t, strings.HasPrefix(errOut, "wavez hook: "), errOut)
			assert.Equal(t, 1, strings.Count(errOut, "\n"), "one line: %q", errOut)
		})
	}
}

// panicking is a stdin whose read panics, standing in for any bug in the
// code that handles an event.
type panicking struct{}

func (panicking) Read([]byte) (int, error) { panic("index out of range [0] with length 0") }

func TestHook_PanicIsOneLineAndStatusOne(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	code := run(t.Context(), []string{"hook"}, stdio{in: panicking{}, out: &out, err: &errOut})

	assert.Equal(t, 1, code, "a Go panic exits 2, which the harness reads as a hook asking to block")
	assert.Empty(t, out.String())
	assert.Equal(t, "wavez hook: internal error: index out of range [0] with length 0\n", errOut.String())
}

func TestHook_HelpDoesNotReadStdin(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--help", "-h", "help"} {
		var out, errOut bytes.Buffer

		// A read here would panic: asking for help must not wait on, or
		// misread, a stdin nobody piped an event into.
		code := run(t.Context(), []string{"hook", flag}, stdio{in: panicking{}, out: &out, err: &errOut})

		assert.Equal(t, 0, code, flag)
		assert.Contains(t, out.String(), "usage: wavez hook < event.json", flag)
		assert.Empty(t, errOut.String(), flag)
	}
}

func TestHook_EventsThatNeedNoGate(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "hook.log")
	t.Setenv(hook.TraceEnv, logPath)
	t.Setenv(hook.AnnounceEnv, "0")

	root := goProject(t)

	for _, name := range []string{"SessionStart", "Stop", "Notification"} {
		stdin := event(t, map[string]any{"session_id": "s1", "cwd": root, "hook_event_name": name})

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
	assert.Contains(t, string(trace), `"note":"stop checks are off"`)
}

func TestHook_SessionStartSentence(t *testing.T) {
	const (
		targeted = "Check your work with the narrowest command that covers it: the tests, build, or " +
			"type check for the files or packages you changed. Do not run whole-project build, test, lint, " +
			"or type-check commands; CI runs those."
		gated = "Format, build and tests for the packages you change run automatically when you finish; " +
			"do not run whole-project build or test commands yourself."
	)

	tests := []struct {
		name   string
		config string
		want   string
		note   string
	}{
		{name: "stop checks off asks for targeted checks", want: targeted, note: "announced targeted checks"},
		{
			name: "stop checks on says they run by themselves", config: "stop_checks = true\n",
			want: gated, note: "announced the gates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "hook.log")
			t.Setenv(hook.TraceEnv, logPath)
			t.Setenv(hook.AnnounceEnv, "")

			root := goProject(t)
			if tt.config != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, ".wavez.toml"), []byte(tt.config), 0o600))
			}

			stdin := event(t, map[string]any{
				"session_id": "s1", "cwd": root, "hook_event_name": "SessionStart", "source": "startup",
			})

			code, out, errOut := invoke(t, stdin, "hook")
			require.Equal(t, 0, code, errOut)

			var answer map[string]map[string]string

			require.NoError(t, json.Unmarshal([]byte(out), &answer), out)
			assert.Equal(t, map[string]map[string]string{"hookSpecificOutput": {
				"hookEventName": "SessionStart", "additionalContext": tt.want,
			}}, answer)

			trace, err := os.ReadFile(logPath) //nolint:gosec // a file this test wrote
			require.NoError(t, err)
			assert.Contains(t, string(trace), `"note":"`+tt.note+`"`)
			assert.Contains(t, string(trace), fmt.Sprintf(`"agent_bytes":%d`, len(tt.want)),
				"the trace carries what the sentence cost")

			t.Setenv(hook.AnnounceEnv, "0")

			code, out, errOut = invoke(t, stdin, "hook")
			assert.Equal(t, 0, code, errOut)
			assert.Empty(t, out, "WAVEZ_ANNOUNCE=0 is the arm of a benchmark that goes without the sentence")
		})
	}
}

func TestHook_EventFromNowhereWritesNothing(t *testing.T) {
	t.Setenv(hook.ProjectDirEnv, "")

	bare := t.TempDir()
	gone := filepath.Join(bare, "nonexistent", "zzz")

	tests := []struct {
		name string
		cwd  string
	}{
		{name: "a cwd that does not exist", cwd: gone},
		{name: "a directory that is no project: no .wavez.toml, no .git, no go.mod", cwd: bare},
	}

	for _, tt := range tests {
		for _, name := range []string{"SessionStart", "PostToolUse", "PreToolUse", "UserPromptSubmit", "Stop"} {
			stdin := event(t, map[string]any{
				"session_id": "x", "cwd": tt.cwd, "hook_event_name": name, "tool_name": "Edit",
				"tool_input": map[string]any{"file_path": "a.go"},
			})

			code, out, errOut := invoke(t, stdin, "hook")
			assert.Equal(t, 0, code, "%s %s: %s", tt.name, name, errOut)
			assert.Empty(t, out)
			assert.Empty(t, errOut)
		}
	}

	entries, err := os.ReadDir(bare)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing was created: no nonexistent/zzz/.wavez, no .wavez beside it")
}

func TestHook_ProjectIsWhereTheSessionStarted(t *testing.T) {
	root := goProject(t)
	nested := filepath.Join(root, "tools")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module tools\n"), 0o600))

	t.Setenv(hook.ProjectDirEnv, root)
	t.Setenv(hook.AnnounceEnv, "0")

	// The agent has run `cd tools`: the event's cwd is now a nested module.
	stdin := event(t, map[string]any{"session_id": "s1", "cwd": nested, "hook_event_name": "SessionStart"})

	code, _, errOut := invoke(t, stdin, "hook")
	require.Equal(t, 0, code, errOut)

	assert.FileExists(t, filepath.Join(root, ".wavez", "sessions", "s1.json"))
	assert.NoDirExists(t, filepath.Join(nested, ".wavez"), "a cd does not start a second project")
}

func TestVersionLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "a binary with no build information", want: "wavez dev (none, unknown)"},
		{
			name: "go install module@v1.0.0, which the linker never stamped",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}},
			want: "wavez 1.0.0 (none, unknown)",
		},
		{
			name: "go build in a checkout",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "12b4f17"},
					{Key: "vcs.time", Value: "2026-11-21T10:00:00Z"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: "wavez dev (12b4f17, 2026-11-21T10:00:00Z)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, versionLine(tt.info))
		})
	}
}

var errRead = errors.New("read failed")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errRead }

func TestHook_StdinThatCannotBeRead(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	code := run(t.Context(), []string{"hook"}, stdio{in: failingReader{}, out: &out, err: &errOut})

	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "read failed")
}

func TestHook_AnnounceFollowsTheFileUnlessTheEnvironmentSaysNo(t *testing.T) {
	tests := []struct {
		name string
		file string
		env  string
		want bool
	}{
		{"no file", "", "", true},
		{"file turns it on", "announce = true\n", "", true},
		{"file turns it off", "announce = false\n", "", false},
		{"environment turns it off over a file that wants it", "announce = true\n", "0", false},
		{"environment turns it off with no file", "", "0", false},
		{"an environment value other than 0 changes nothing", "announce = false\n", "1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(hook.AnnounceEnv, tt.env)

			root := goProject(t)
			if tt.file != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, ".wavez.toml"), []byte(tt.file), 0o600))
			}

			stdin := event(t, map[string]any{
				"session_id": "s1", "cwd": root, "hook_event_name": "SessionStart", "source": "startup",
			})

			code, out, errOut := invoke(t, stdin, "hook")
			require.Equal(t, 0, code, errOut)
			assert.Equal(t, tt.want, out != "", out)
		})
	}
}

func TestStopBudget_EnvironmentWinsOverTheFile(t *testing.T) {
	tests := []struct {
		name string
		env  string
		file time.Duration
		want time.Duration
	}{
		{"neither", "", 0, 0},
		{"file only", "", time.Minute, time.Minute},
		{"environment only", "45s", 0, 45 * time.Second},
		{"environment wins", "45s", time.Minute, 45 * time.Second},
		{"an environment value that is not a duration falls back to the file", "soon", time.Minute, time.Minute},
		{"a non-positive environment value falls back to the file", "0s", time.Minute, time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(stopBudgetEnv, tt.env)

			assert.Equal(t, tt.want, stopBudget(config.Config{StopBudget: tt.file}))
			assert.Equal(t, tt.want > 0, len(gateOptions(config.Config{StopBudget: tt.file})) == 1)
		})
	}
}
