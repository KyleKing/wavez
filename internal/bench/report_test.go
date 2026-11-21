package bench_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/transcript"
)

// runs makes n records of one task and arm with the given turns, a cost and
// wall clock that scale with them, and that many sweeps.
func runs(n int, arm bench.Arm, served string, turns, sweeps int) []bench.Record {
	out := make([]bench.Record, 0, n)

	for range n {
		out = append(out, bench.Record{
			TaskID: "e1", TaskHash: "hash1", Arm: arm, Harness: "claude", Served: served,
			Stop: bench.StopCompleted, Pass: true, Turns: turns, CostUSD: float64(turns) / 100,
			WallMS: int64(turns) * 1000, Transcript: &transcript.Stats{Sweeps: sweeps},
		})
	}

	return out
}

func report(t *testing.T, recs []bench.Record, task string) string {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, bench.Report(&out, recs, task))

	return out.String()
}

func TestReport_NoiseRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		recs    []bench.Record
		want    []string
		wantNot []string
	}{
		{
			name: "enough runs and a large ratio is a result",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 4), runs(5, bench.ArmWavez, "m", 4, 0)...),
			want: []string{
				"wavez/baseline on claude m, 5 vs 5 runs",
				"turns    0.40x  outside the noise",
				"cost     0.40x  outside the noise",
				"sweeps   0.00x  outside the noise",
			},
			wantNot: []string{"inside the noise", "fewer than"},
		},
		{
			name: "enough runs and a small ratio is noise",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 2), runs(6, bench.ArmWavez, "m", 7, 2)...),
			want: []string{
				"turns    0.70x  inside the noise (between 0.5x and 2x)",
				"sweeps   1.00x  inside the noise (between 0.5x and 2x)",
			},
			wantNot: []string{"outside the noise", "fewer than"},
		},
		{
			name: "a ratio on the boundary is a result",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 1), runs(5, bench.ArmWavez, "m", 20, 1)...),
			want: []string{"turns    2.00x  outside the noise"},
		},
		{
			name: "too few runs is noise however large the ratio",
			recs: append(runs(4, bench.ArmBaseline, "m", 10, 4), runs(5, bench.ArmWavez, "m", 2, 0)...),
			want: []string{
				"wavez/baseline on claude m, 5 vs 4 runs",
				"turns    0.20x  inside the noise (too few runs)",
				"fewer than 5 runs per arm: nothing above is a result yet",
			},
			wantNot: []string{"outside the noise"},
		},
		{
			name: "zero in both arms has no ratio",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 0), runs(5, bench.ArmWavez, "m", 10, 0)...),
			want: []string{"sweeps      -   zero in both arms"},
		},
		{
			name: "a zero baseline has no ratio",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 0), runs(5, bench.ArmWavez, "m", 10, 3)...),
			want: []string{"sweeps      -   baseline is zero, so there is no ratio"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := report(t, tt.recs, "")
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}

			for _, not := range tt.wantNot {
				assert.NotContains(t, got, not)
			}
		})
	}
}

func TestReport_RefusesUnlikeArms(t *testing.T) {
	t.Parallel()

	otherHarness := runs(5, bench.ArmWavez, "m", 4, 0)
	otherTask := runs(5, bench.ArmWavez, "m", 4, 0)

	for i := range otherHarness {
		otherHarness[i].Harness = "pi"
		otherTask[i].TaskHash = "hash2"
	}

	tests := []struct {
		name string
		want string
		recs []bench.Record
	}{
		{
			name: "different served models",
			recs: append(runs(5, bench.ArmBaseline, "model-a", 10, 4), runs(5, bench.ArmWavez, "model-b", 4, 0)...),
			want: "no comparison: baseline was served model-a and wavez was served model-b",
		},
		{
			name: "one side has no served model",
			recs: append(runs(5, bench.ArmBaseline, "", 10, 4), runs(5, bench.ArmWavez, "model-b", 4, 0)...),
			want: "no comparison: baseline was served - and wavez was served model-b",
		},
		{
			name: "different harnesses",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 4), otherHarness...),
			want: "no comparison: baseline ran under claude and wavez under pi",
		},
		{
			name: "the task changed",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 4), otherTask...),
			want: "no comparison: the task changed between the arms (hash1 then hash2)",
		},
		{
			name: "only one arm",
			recs: runs(5, bench.ArmBaseline, "m", 10, 4),
			want: "no comparison: only one arm has runs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := report(t, tt.recs, "e1")
			assert.Contains(t, got, tt.want)
			assert.NotContains(t, got, "wavez/baseline on")
			assert.NotContains(t, got, "x  ")
		})
	}
}

func TestReport_ComparesOnlyTheSharedModel(t *testing.T) {
	t.Parallel()

	recs := append(runs(5, bench.ArmBaseline, "model-a", 10, 4), runs(5, bench.ArmWavez, "model-a", 4, 0)...)
	recs = append(recs, runs(2, bench.ArmWavez, "model-b", 1, 0)...)

	got := report(t, recs, "e1")
	assert.Equal(t, 1, strings.Count(got, "wavez/baseline on"))
	assert.Contains(t, got, "wavez/baseline on claude model-a, 5 vs 5 runs")
	assert.Contains(t, got, "turns    0.40x  outside the noise")
}

func TestReport_Table(t *testing.T) {
	t.Parallel()

	recs := runs(3, bench.ArmBaseline, "claude-example-main", 4, 2)
	recs[0].Turns, recs[0].CostUSD, recs[0].WallMS = 2, 0.02, 2500
	recs[1].Pass = false
	recs[2].Stop, recs[2].Turns, recs[2].Pass = bench.StopHarnessError, 900, false

	bare := bench.Record{TaskID: "q1", TaskHash: "hash9", Arm: bench.ArmWavez, Harness: "pi", WallMS: 61000}
	recs = append(recs, bare, bare)

	want := strings.Join([]string{
		"task e1",
		"arm       harness  served                       task hash     runs   pass  turns      cost     wall  sweeps",
		"baseline  claude   claude-example-main          hash1            3    1/3      3   $0.0300     3.3s       2",
		"no comparison: only one arm has runs",
		"",
		"task q1",
		"arm       harness  served                       task hash     runs   pass  turns      cost     wall  sweeps",
		"wavez     pi       -                            hash9            2    0/2      -         -     1m1s       -",
		"no comparison: only one arm has runs",
		"",
	}, "\n")
	assert.Equal(t, want, report(t, recs, ""))

	only := report(t, recs, "q1")
	assert.NotContains(t, only, "task e1")
	assert.Contains(t, only, "task q1")
}

func TestReport_NothingRecorded(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "no runs recorded\n", report(t, nil, ""))
	assert.Equal(t, "no runs recorded for task h3\n", report(t, runs(1, bench.ArmBaseline, "m", 1, 0), "h3"))
}

type failingWriter struct{}

var errWrite = errors.New("pipe closed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestReport_ReportsAWriteFailure(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, bench.Report(failingWriter{}, nil, ""), errWrite)
}
