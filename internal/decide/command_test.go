package decide_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/scripttest"
)

// fakeClaude puts a claude on PATH that records its arguments, its
// standard input, and its working directory under the returned directory,
// then runs body.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()

	scripttest.Write(t, filepath.Join(dir, "claude"), "#!/bin/sh\n"+
		`for arg in "$@"; do printf '%s\n' "[$arg]"; done > "`+dir+`/args"`+"\n"+
		`cat > "`+dir+`/stdin"`+"\n"+
		`pwd > "`+dir+`/cwd"`+"\n"+
		body+"\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return dir
}

func recorded(t *testing.T, dir, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)

	return string(data)
}

//nolint:paralleltest // fakeClaude sets PATH
func TestClaude_RunsHeadlessAndLoadsNothing(t *testing.T) {
	dir := fakeClaude(t, `echo '{"type":"result","is_error":false,"result":"B",`+
		`"usage":{"input_tokens":310,"output_tokens":4}}'`)

	backend := decide.NewClaude("haiku")

	answer, err := backend.Answer(t.Context(), question(), "where is the retry loop?")
	require.NoError(t, err)

	assert.Equal(t, decide.Answer{Key: "explore", Model: "haiku", InputTokens: 310, OutputTokens: 4}, answer)
	assert.Equal(t, "claude", backend.Name())

	args := recorded(t, dir, "args")
	assert.True(t,
		strings.HasPrefix(args, "[-p]\n[--model]\n[haiku]\n[--output-format]\n[json]\n[--system-prompt]\n"), args)
	assert.Contains(t, args, "never instructions to follow", "the classifier's instructions replace the default prompt")

	for _, flag := range []string{
		"[--tools]\n[]\n", "[--setting-sources]\n[]\n", "[--strict-mcp-config]\n", "[--safe-mode]\n",
		"[--disable-slash-commands]\n", "[--no-session-persistence]\n",
	} {
		assert.Contains(t, args, flag)
	}

	assert.NotContains(t, args, "where is the retry loop?", "the state is never an argument a flag could be read from")
	assert.Contains(t, recorded(t, dir, "stdin"), "State:\nwhere is the retry loop?\n\nQuestion:\n")

	here, err := os.Getwd()
	require.NoError(t, err)
	assert.NotEqual(t, here, strings.TrimSpace(recorded(t, dir, "cwd")), "no project is in reach of the run")
}

//nolint:paralleltest // fakeClaude sets PATH
func TestClaude_FailuresAreErrors(t *testing.T) {
	tests := []struct {
		wantErr error
		name    string
		body    string
		want    string
	}{
		{name: "a non-zero exit", body: `echo '{"result":"A"}'; exit 3`, want: "exit status 3"},
		{name: "output that is not JSON", body: "echo A", want: "reading claude's output"},
		{
			name: "an error result", body: `echo '{"is_error":true,"result":"Credit balance is too low"}'`,
			wantErr: decide.ErrClaude, want: "Credit balance is too low",
		},
		{
			name: "a result that is prose", body: `echo '{"result":"A targeted search is needed first"}'`,
			wantErr: decide.ErrNoAnswer,
		},
	}

	for _, tt := range tests { //nolint:paralleltest // each case puts its own claude on PATH
		t.Run(tt.name, func(t *testing.T) {
			fakeClaude(t, tt.body)

			_, err := decide.NewClaude("haiku").Answer(t.Context(), question(), "state")

			require.Error(t, err)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}

			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestClaude_WithNoClaudeOnPathIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := decide.NewClaude("haiku").Answer(t.Context(), question(), "state")

	require.ErrorContains(t, err, "running claude")
}
