package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeProject is a Go module with one file a prompt can name. The commands
// under test read the working directory, so each test moves into it.
func routeProject(t *testing.T, config string) string {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.26\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package m\n\nfunc Read() {}\n"), 0o600))

	if config != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, ".wavez.toml"), []byte(config), 0o600))
	}

	return root
}

func TestRoute_OnePrompt(t *testing.T) {
	t.Parallel()

	root := routeProject(t, "")

	code, out, errOut := invoke(t, "", "route", "--dir", root, "In", "a.go", "return early when n is 0.")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "choice   direct\nbackend  rule\n"+
		"hint     wavez: this prompt names what to change and where, "+
		"so make the change without surveying the repository first.\n", out)

	code, out, errOut = invoke(t, "", "route", "--dir", root, "Make startup faster.")
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "choice   silent\nbackend  fallback\n")
	assert.Contains(t, out, "hint     (none: the agent is told nothing)\n")
}

func TestRoute_Eval(t *testing.T) {
	t.Parallel()

	root := routeProject(t, "")

	corpus := filepath.Join(root, "corpus.jsonl")

	require.NoError(t, os.WriteFile(corpus, []byte(
		`{"state": "In a.go return early.\n\nexisting-paths: a.go", "want": "direct"}`+"\n"+
			`{"state": "Where is the limiter set", "want": "explore"}`+"\n"+
			`{"state": "Make startup faster.", "want": "explore"}`+"\n",
	), 0o600))

	code, out, errOut := invoke(t, "", "route", "--dir", root, "eval", corpus)
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "question: route\ncases: 3  correct: 2")
	assert.Regexp(t, `rule +2 +2 +100\.0%`, out)
	assert.Regexp(t, `explore +silent +fallback +Make startup faster\.`, out)
}

func TestRoute_Failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config string
		want   string
		args   []string
	}{
		{name: "no prompt", args: []string{}, want: "usage:"},
		{name: "help", args: []string{"--help"}, want: "eval <corpus.jsonl>"},
		{name: "eval without a corpus", args: []string{"eval"}, want: "usage:"},
		{name: "eval of a missing corpus", args: []string{"eval", "none.jsonl"}, want: "opening the corpus"},
		{name: "eval of a corpus that is not one", args: []string{"eval", "go.mod"}, want: "go.mod: "},
		{
			name: "a configuration that does not load", args: []string{"x"}, config: "nope = 1\n",
			want: "loading configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := routeProject(t, tt.config)
			args := []string{"route", "--dir", root}

			// A corpus is named relative to the project, wherever the test runs.
			for i, arg := range tt.args {
				if i > 0 && tt.args[0] == "eval" {
					arg = filepath.Join(root, arg)
				}

				args = append(args, arg)
			}

			code, out, errOut := invoke(t, "", args...)
			assert.Equal(t, 1, code)
			assert.Empty(t, out)
			assert.Contains(t, errOut, tt.want)
		})
	}
}

func TestHook_RouteHintFollowsTheConfiguration(t *testing.T) {
	t.Parallel()

	event := func(root string) string {
		return `{"session_id": "s1", "cwd": "` + root + `", "hook_event_name": "UserPromptSubmit", ` +
			`"prompt": "In a.go return early when n is 0."}`
	}

	t.Run("off by default", func(t *testing.T) {
		t.Parallel()

		root := routeProject(t, "")

		code, out, errOut := invoke(t, event(root), "hook")
		assert.Equal(t, 0, code, errOut)
		assert.Empty(t, out, "a project that has not turned the hint on hears nothing")
	})

	t.Run("on", func(t *testing.T) {
		t.Parallel()

		root := routeProject(t, "route = true\n")

		code, out, errOut := invoke(t, event(root), "hook")
		assert.Equal(t, 0, code, errOut)
		assert.JSONEq(t, `{"hookSpecificOutput": {"hookEventName": "UserPromptSubmit", "additionalContext": `+
			`"wavez: this prompt names what to change and where, `+
			`so make the change without surveying the repository first."}}`, out)
	})

	t.Run("a configuration that does not load leaves it off", func(t *testing.T) {
		t.Parallel()

		root := routeProject(t, "route = true\nnope = 1\n")

		code, out, _ := invoke(t, event(root), "hook")
		assert.Equal(t, 0, code)
		assert.Empty(t, out)
	})
}

func TestHook_DeniesARenameByText(t *testing.T) {
	root := routeProject(t, "")

	// The denial needs a gopls on PATH, and any program under that name
	// will do, because the hook only looks for it.
	shell, err := exec.LookPath("sh")
	require.NoError(t, err)

	bin := t.TempDir()
	require.NoError(t, os.Symlink(shell, filepath.Join(bin, "gopls")))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	stdin := `{"session_id": "s1", "cwd": "` + root + `", "hook_event_name": "PreToolUse", "tool_name": "Bash", ` +
		`"tool_input": {"command": "sed -i 's/Read/ReadLog/g' a.go"}}`

	code, out, errOut := invoke(t, stdin, "hook")
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, `"permissionDecision":"deny"`)
	assert.Contains(t, out, `wavez rename a.go:Read ReadLog`)

	code, out, errOut = invoke(t, stdin, "hook")
	require.Equal(t, 0, code, errOut)
	assert.Empty(t, out, "the same command sent again runs")
}
