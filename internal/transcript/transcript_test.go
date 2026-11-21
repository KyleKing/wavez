package transcript_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/transcript"
)

const fixtureSession = "11111111-2222-3333-4444-555555555555"

func TestRead_AttributesTheFixture(t *testing.T) {
	t.Parallel()

	f, err := os.Open(filepath.Join("testdata", "session.jsonl"))
	require.NoError(t, err)

	t.Cleanup(func() { assert.NoError(t, f.Close()) })

	got, err := transcript.Read(f)
	require.NoError(t, err)

	want := transcript.Stats{
		ToolCalls:     map[string]int{"Read": 3, "Bash": 3, "Edit": 1, "TodoWrite": 1},
		ToolErrors:    map[string]int{"Bash": 1},
		SweepCommands: map[string]int{"go test": 1},
		Refactors:     map[string]int{"rename": 1},
		Turns:         8,
		Retrieval:     2,
		Edit:          1,
		Shell:         2,
		Other:         1,
		Prose:         2,
		RepeatReads:   1,
		Sweeps:        1,
		StopBlocks:    1,
		Skipped:       1,
	}
	assert.Equal(t, want, got)
	assert.Equal(t, 8, got.TotalToolCalls())
	assert.Equal(t, 1, got.TotalToolErrors())
	assert.Equal(t, 1, got.TotalRefactors())
}

func TestReadSession_FoldsInSubagents(t *testing.T) {
	t.Parallel()

	got, err := transcript.ReadSession(filepath.Join("testdata", "session.jsonl"))
	require.NoError(t, err)

	assert.Equal(t, 1, got.Subagents)
	assert.Equal(t, 11, got.Turns)
	assert.Equal(t, 3, got.Retrieval)
	assert.Equal(t, 3, got.Shell)
	assert.Equal(t, 2, got.Sweeps)
	assert.Equal(t, map[string]int{"go test": 1, "pytest": 1}, got.SweepCommands)
	assert.Equal(t, 1, got.ToolCalls["Grep"])
}

func TestReadSession_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := transcript.ReadSession(filepath.Join(t.TempDir(), "absent.jsonl"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRead_Edges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  transcript.Stats
	}{
		{name: "empty", input: "", want: transcript.Stats{}},
		{name: "blank lines only", input: "\n\n", want: transcript.Stats{}},
		{
			name: "no trailing newline", input: `{"type":"assistant","message":{"id":"m","content":[]}}`,
			want: transcript.Stats{Turns: 1, Prose: 1},
		},
		{
			name: "prevented continuation is a stop block",
			input: `{"type":"system","subtype":"stop_hook_summary","preventedContinuation":true}` + "\n" +
				`{"type":"system","subtype":"turn_duration"}` + "\n",
			want: transcript.Stats{StopBlocks: 1},
		},
		{
			name: "error for a call never seen",
			input: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x","is_error":true}]}}` +
				"\n",
			want: transcript.Stats{ToolErrors: map[string]int{"unknown": 1}},
		},
		{
			name: "notebook edit resets the repeat read",
			input: strings.Join([]string{
				use("m1", "t1", "Read", `{"file_path":"n.ipynb"}`),
				use("m2", "t2", "NotebookEdit", `{"notebook_path":"n.ipynb"}`),
				use("m3", "t3", "Read", `{"file_path":"n.ipynb"}`),
				use("m4", "t4", "Read", `{"file_path":"n.ipynb"}`),
			}, "\n"),
			want: transcript.Stats{
				ToolCalls: map[string]int{"Read": 3, "NotebookEdit": 1},
				Turns:     4, Retrieval: 3, Edit: 1, RepeatReads: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transcript.Read(strings.NewReader(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func use(msg, id, tool, input string) string {
	return `{"type":"assistant","message":{"id":"` + msg + `","content":[{"type":"tool_use","id":"` + id +
		`","name":"` + tool + `","input":` + input + `}]}}`
}

type failingReader struct{}

var errRead = errors.New("disk gone")

func (failingReader) Read([]byte) (int, error) { return 0, errRead }

func TestRead_ReportsAReadFailure(t *testing.T) {
	t.Parallel()

	_, err := transcript.Read(failingReader{})
	require.ErrorIs(t, err, errRead)
}

func TestFind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := filepath.Join(root, "-work-project")
	require.NoError(t, os.MkdirAll(project, 0o750))

	path := filepath.Join(project, fixtureSession+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))

	tests := []struct {
		name    string
		session string
		want    string
	}{
		{name: "found by id", session: fixtureSession, want: path},
		{name: "unknown id", session: "99999999-0000-0000-0000-000000000000"},
		{name: "empty id", session: ""},
		{name: "glob characters are refused", session: "*"},
		{name: "path separators are refused", session: "../" + fixtureSession},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := transcript.Find(root, tt.session)
			if tt.want == "" {
				var missing *transcript.NotFoundError

				require.ErrorAs(t, err, &missing)
				assert.Equal(t, tt.session, missing.Session)
				assert.Contains(t, err.Error(), root)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStats_Render(t *testing.T) {
	t.Parallel()

	stats, err := transcript.ReadSession(filepath.Join("testdata", "session.jsonl"))
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, stats.Render(&out))

	want := strings.Join([]string{
		"turns         11 (1 subagent transcript(s) included)",
		"  retrieval   3",
		"  edit        1",
		"  shell       3",
		"  other       1",
		"  prose       3",
		"tool calls    10 (Bash 4, Read 3, Edit 1, Grep 1, TodoWrite 1)",
		"tool errors   1 (Bash 1)",
		"repeat reads  1",
		"sweeps        2 (go test 1, pytest 1)",
		"refactors     1 (rename 1)",
		"stop blocks   1",
		"skipped       1 unreadable line(s)",
		"",
	}, "\n")
	assert.Equal(t, want, out.String())
}

type failingWriter struct{}

var errWrite = errors.New("pipe closed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestStats_RenderReportsAWriteFailure(t *testing.T) {
	t.Parallel()

	var stats transcript.Stats

	require.ErrorIs(t, stats.Render(failingWriter{}), errWrite)
}
