package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/scripttest"
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

	code, out, errOut := invoke(t, "", "route", "--dir", root, "Rename", "Read", "in", "a.go", "to", "ReadLog.")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "choice   direct\nbackend  rule\n"+
		"hint     wavez: this prompt names what to change and where, "+
		"so make the change without surveying the repository first.\n", out)

	code, out, errOut = invoke(t, "", "route", "--dir", root, "In a.go return early when n is 0.")
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "choice   silent\nbackend  fallback\nreason   no backend\n")
	assert.Contains(t, out, "hint     (none: the agent is told nothing)\n")
}

func TestRoute_AProjectEndpointOffThisMachineIsNamedAsTheReason(t *testing.T) {
	t.Setenv("WAVEZ_DECIDE_ENDPOINT", "")

	root := routeProject(t, "route = true\n\n[decide]\nendpoint = \"https://models.example.com/v1\"\nmodel = \"m\"\n")

	code, out, errOut := invoke(t, "", "route", "--dir", root, "Make startup faster.")
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "choice   silent\nbackend  fallback\n"+
		"reason   the project's endpoint is not loopback and WAVEZ_DECIDE_ENDPOINT is not set\n")
}

const routeCases = `{"state": "Rename Read in a.go to ReadLog.\n\nexisting-paths: a.go", "want": "direct"}
{"state": "Where is the limiter set", "want": "explore"}
{"state": "Locate the code that builds the query.", "want": "silent"}
{"state": "Make startup faster.", "want": "explore"}
{"state": "thanks", "want": "silent"}
`

func TestRoute_Eval(t *testing.T) {
	t.Parallel()

	root := routeProject(t, "")
	corpus := filepath.Join(root, "corpus.jsonl")

	require.NoError(t, os.WriteFile(corpus, []byte(routeCases), 0o600))

	code, out, errOut := invoke(t, "", "route", "--dir", root, "eval", corpus)
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "question: route\ncases: 5\n")
	assert.Contains(t, out, "rules: answered 3 of 5 (coverage 60.0%); 2 right, 1 wrong (precision 66.7%)\n")
	assert.Contains(t, out, "the 2 cases the rules left:\n  fallback: 2 got \"silent\"; 1 right\n    2: no backend\n")
	assert.Regexp(t,
		`wrong by rule:\nwant +got +state\nsilent +explore +Locate the code that builds the query\.\n`, out)
	assert.Regexp(t, `wrong by fallback:\nwant +got +state\nexplore +silent +Make startup faster\.\n`, out)
}

func TestRoute_DoesNotWriteTheDecisionLog(t *testing.T) {
	t.Parallel()

	root := routeProject(t, "route = true\n")
	corpus := filepath.Join(root, "corpus.jsonl")

	require.NoError(t, os.WriteFile(corpus, []byte(routeCases), 0o600))

	code, _, errOut := invoke(t, "", "route", "--dir", root, "Rename", "Read", "in", "a.go", "to", "ReadLog.")
	require.Equal(t, 0, code, errOut)

	code, _, errOut = invoke(t, "", "route", "--dir", root, "eval", corpus)
	require.Equal(t, 0, code, errOut)

	assert.NoDirExists(t, filepath.Join(root, ".wavez"), "the log holds what the hook saw and nothing asked by hand")

	event := `{"session_id": "s1", "cwd": "` + root + `", "hook_event_name": "UserPromptSubmit", "prompt": "thanks"}`

	code, _, errOut = invoke(t, event, "hook")
	require.Equal(t, 0, code, errOut)
	assert.FileExists(t, filepath.Join(root, ".wavez", "decisions.jsonl"), "the hook is what writes it")
}

func TestRoute_EvalWithClaudeSaysHowManyRequestsItBillsBeforeMakingThem(t *testing.T) {
	root := routeProject(t, "")
	corpus := filepath.Join(root, "corpus.jsonl")

	require.NoError(t, os.WriteFile(corpus, []byte(routeCases), 0o600))

	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")

	// The fake answers explore to everything and notes each run.
	scripttest.Write(t, filepath.Join(bin, "claude"), "#!/bin/sh\ncat > /dev/null\n"+
		`echo "$3" >> "`+calls+`"`+"\n"+
		`echo '{"is_error":false,"result":"B","usage":{"input_tokens":300,"output_tokens":1}}'`+"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	code, out, errOut := invoke(t, "", "route", "--dir", root, "eval", "--claude", "haiku", corpus)
	require.Equal(t, 0, code, errOut)

	assert.True(t, strings.HasPrefix(out,
		"asking claude (haiku) about the 2 of 5 cases the rules leave, 4 at a time\nquestion: route\n"), out)
	assert.Contains(t, out, "rules: answered 3 of 5 (coverage 60.0%); 2 right, 1 wrong (precision 66.7%)\n")
	assert.Contains(t, out, "  claude haiku: answered 2; 1 right, 1 wrong (50.0%)\n")
	assert.Contains(t, out, "  with no model: \"silent\" for all 2 would be right 1 times (50.0%)\n")
	assert.Regexp(t, `wrong by claude:\nwant +got +state\nsilent +explore +thanks\n`, out)

	ran, err := os.ReadFile(calls) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, []string{"haiku", "haiku"}, strings.Fields(string(ran)),
		"claude is run once per case the rules left, and for no other")
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
		{name: "help", args: []string{"--help"}, want: "eval --claude MODEL <corpus.jsonl>"},
		{name: "eval with a flag it does not have", args: []string{"eval", "--gpt", "x.jsonl"}, want: "usage:"},
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
			`"prompt": "Rename Read in a.go to ReadLog."}`
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
