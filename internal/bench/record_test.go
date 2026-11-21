package bench_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/transcript"
)

func sampleRecord(id string) bench.Record {
	return bench.Record{
		TaskID: id, TaskHash: "9ec73cf6848e", Arm: bench.ArmBaseline, Harness: "claude",
		Model: "haiku", Served: "claude-example-main", Rev: rev, WavezVersion: "dev",
		Started: "2026-01-01T00:00:00Z", Stop: bench.StopCompleted, SessionID: "s1",
		Transcript: &transcript.Stats{ToolCalls: map[string]int{"Edit": 1}, Turns: 3, Edit: 1, Prose: 2},
		Checks: []bench.CheckResult{
			{Check: "README.md:x", Pass: true}, {Check: "build:./...", Detail: "undefined: y"},
		},
		Usage: bench.Usage{Input: 6, Output: 377, CacheRead: 124902, CacheCreation: 19608},
		Turns: 3, CostUSD: 0.0054, WallMS: 4796, HarnessMS: 3520,
	}
}

func TestRecords_AppendAndLoad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "records.jsonl")

	none, err := bench.LoadRecords(path)
	require.NoError(t, err)
	assert.Empty(t, none)

	first, second := sampleRecord("e1"), sampleRecord("e2")
	second.Transcript, second.Pass = nil, true

	require.NoError(t, bench.AppendRecord(path, &first))

	before := read(t, path)

	require.NoError(t, bench.AppendRecord(path, &second))

	after := read(t, path)
	assert.True(t, strings.HasPrefix(after, before), "the first line must not be rewritten")
	assert.Equal(t, 2, strings.Count(after, "\n"))
	assert.True(t, strings.HasPrefix(after, `{"task_id":"e1","task_hash":"9ec73cf6848e","arm":"baseline",`), after)

	got, err := bench.LoadRecords(path)
	require.NoError(t, err)
	assert.Equal(t, []bench.Record{first, second}, got)
}

func TestLoadRecords_Edges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
		want    int
	}{
		{name: "blank lines are skipped", body: "\n{\"task_id\":\"a\"}\n\n{\"task_id\":\"b\"}\n", want: 2},
		{name: "no trailing newline", body: `{"task_id":"a"}`, want: 1},
		{name: "empty file", body: "", want: 0},
		{name: "a bad line names its number", body: "{\"task_id\":\"a\"}\n{oops\n", wantErr: "records line 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "records.jsonl")
			write(t, path, tt.body)

			got, err := bench.LoadRecords(path)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.want)
		})
	}
}

func TestRecords_UnusablePaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	write(t, blocker, "x")

	rec := sampleRecord("e1")

	require.Error(t, bench.AppendRecord(filepath.Join(blocker, "records.jsonl"), &rec))
	require.Error(t, bench.AppendRecord(dir, &rec))

	_, err := bench.LoadRecords(filepath.Join(blocker, "records.jsonl"))
	require.Error(t, err)
	require.NotErrorIs(t, err, os.ErrNotExist)
}
