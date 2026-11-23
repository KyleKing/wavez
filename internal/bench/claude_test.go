package bench_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

// fakeClaude writes a claude stand-in that records its arguments and its
// working directory next to itself, prints stdout, and exits with code.
func fakeClaude(t *testing.T, stdout string, code int) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "result.json")
	write(t, out, stdout)

	return script(t, "claude", `printf '%s\n' "$@" > "$0.args"
pwd > "$0.cwd"
echo "$BENCH_MARK" > "$0.env"
echo "a warning on stderr" >&2
cat '`+out+`'
exit `+strconv.Itoa(code)+"\n")
}

func canned(t *testing.T, name string) string {
	t.Helper()

	return read(t, filepath.Join("testdata", name))
}

func TestClaude_ParsesTheResultObject(t *testing.T) {
	t.Parallel()

	bin := fakeClaude(t, canned(t, "claude_success.json"), 0)
	dir := t.TempDir()
	harness := &bench.Claude{Bin: bin}

	got, err := harness.Run(t.Context(), bench.Request{
		Dir: dir, Prompt: "Change 'one thing' and nothing else.", Model: "haiku",
		SessionID: fixtureSession, Env: []string{"BENCH_MARK=marked", "PATH=/usr/bin:/bin"},
	})
	require.NoError(t, err)

	want := bench.Outcome{
		Answer:    "Done. The status line now reads as asked.",
		SessionID: "11111111-2222-3333-4444-555555555555",
		Served:    "claude-example-main",
		Stop:      bench.StopCompleted,
		Usage:     bench.Usage{Input: 6, Output: 377, CacheRead: 124902, CacheCreation: 19608},
		Turns:     3,
		CostUSD:   0.0125,
		Duration:  3520 * time.Millisecond,
	}
	assert.Equal(t, want, got)

	args := strings.Split(strings.TrimSpace(read(t, bin+".args")), "\n")
	assert.Equal(t, []string{
		"-p", "Change 'one thing' and nothing else.",
		"--output-format", "json",
		"--permission-mode", "acceptEdits",
		"--allowedTools", "Bash,Read,Edit,Write,MultiEdit,NotebookEdit,Glob,Grep,TodoWrite,Task,Agent,Skill",
		"--strict-mcp-config",
		"--setting-sources", "project,local",
		"--session-id", fixtureSession,
		"--model", "haiku",
	}, args)
	assert.Equal(t, "marked\n", read(t, bin+".env"))

	cwd, err := filepath.EvalSymlinks(strings.TrimSpace(read(t, bin+".cwd")))
	require.NoError(t, err)

	wantDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, wantDir, cwd)
}

func TestClaude_OptionalFlags(t *testing.T) {
	t.Parallel()

	bin := fakeClaude(t, canned(t, "claude_success.json"), 0)
	harness := &bench.Claude{Bin: bin, UserSettings: true}

	_, err := harness.Run(t.Context(), bench.Request{Dir: t.TempDir(), Prompt: "p"})
	require.NoError(t, err)

	args := read(t, bin+".args")
	assert.NotContains(t, args, "--setting-sources")
	assert.NotContains(t, args, "--model")
	assert.NotContains(t, args, "--session-id")
}

func TestClaude_Outcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stdout      string
		wantStop    string
		wantSession string
		wantServed  string
		code        int
		wantErr     bool
	}{
		{
			name: "an error result with a non-zero exit is still a run", stdout: canned(t, "claude_error.json"),
			code: 1, wantStop: "error_max_turns", wantSession: "s-err", wantServed: "claude-example-main",
		},
		{
			name: "is_error without a subtype", stdout: `{"type":"result","is_error":true,"session_id":"s"}`,
			wantStop: bench.StopError, wantSession: "s",
		},
		{
			name:   "no session in the result falls back to the one asked for",
			stdout: `{"type":"result","subtype":"success"}`, wantStop: bench.StopCompleted, wantSession: "asked",
		},
		{
			name: "served model ties break on output tokens then name",
			stdout: `{"type":"result","subtype":"success","modelUsage":` +
				`{"b":{"outputTokens":5},"a":{"outputTokens":5},"c":{"outputTokens":1}}}`,
			wantStop: bench.StopCompleted, wantSession: "asked", wantServed: "a",
		},
		{name: "not json", stdout: "Credit balance is too low", code: 1, wantErr: true, wantSession: "asked"},
		{name: "json that is not a result", stdout: `{"type":"system"}`, wantErr: true, wantSession: "asked"},
		{name: "nothing printed", stdout: "", code: 1, wantErr: true, wantSession: "asked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := &bench.Claude{Bin: fakeClaude(t, tt.stdout, tt.code)}

			got, err := harness.Run(t.Context(), bench.Request{Dir: t.TempDir(), Prompt: "p", SessionID: "asked"})
			assert.Equal(t, tt.wantSession, got.SessionID)

			if tt.wantErr {
				var bad *bench.ResultError

				require.ErrorAs(t, err, &bad)
				assert.Contains(t, err.Error(), "no result object")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantStop, got.Stop)
			assert.Equal(t, tt.wantServed, got.Served)
		})
	}
}

func TestClaude_MissingBinary(t *testing.T) {
	t.Parallel()

	harness := &bench.Claude{Bin: filepath.Join(t.TempDir(), "absent")}

	_, err := harness.Run(t.Context(), bench.Request{Dir: t.TempDir(), Prompt: "p"})
	require.Error(t, err)
}
