package bench_test

import (
	"bytes"
	"errors"
	"strconv"
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

// spread makes one record per turn count, served by m with no sweeps.
func spread(arm bench.Arm, turns ...int) []bench.Record {
	out := make([]bench.Record, 0, len(turns))
	for _, n := range turns {
		out = append(out, runs(1, arm, "m", n, 0)...)
	}

	return out
}

// stopped makes n records of wavez runs that never completed and so report
// nothing they spent.
func stopped(n int, served, stop string) []bench.Record {
	out := runs(n, bench.ArmWavez, served, 0, 0)
	for i := range out {
		out[i].Stop, out[i].Pass, out[i].Transcript = stop, false, nil
	}

	return out
}

// each returns the records after edit has been applied to every one.
func each(recs []bench.Record, edit func(r *bench.Record)) []bench.Record {
	for i := range recs {
		edit(&recs[i])
	}

	return recs
}

func report(t *testing.T, recs []bench.Record, task string) string {
	t.Helper()

	return reportAgainst(t, recs, bench.ReportOptions{Task: task})
}

func reportAgainst(t *testing.T, recs []bench.Record, opts bench.ReportOptions) string {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, bench.Report(&out, recs, opts))

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
			name: "five runs an arm with no overlap is a result",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 4), runs(5, bench.ArmWavez, "m", 4, 0)...),
			want: []string{
				"wavez/baseline, 5 vs 5 runs",
				"turns    0.40x   n 5 vs 5  p 0.008  outside the noise",
				"cost     0.40x   n 5 vs 5  p 0.008  outside the noise",
				"sweeps   0.00x   n 5 vs 5  p 0.008  outside the noise",
			},
			wantNot: []string{"inside the noise", "no verdict"},
		},
		{
			name: "a quarter fewer turns on tight runs is a result",
			recs: append(spread(bench.ArmBaseline, 20, 22, 19, 21, 23), spread(bench.ArmWavez, 15, 16, 14, 17, 16)...),
			want: []string{"turns    0.76x   n 5 vs 5  p 0.008  outside the noise"},
		},
		{
			name: "half the turns on runs that overlap is noise",
			recs: append(spread(bench.ArmBaseline, 4, 30, 10, 28, 6), spread(bench.ArmWavez, 5, 3, 29, 5, 27)...),
			want: []string{"turns    0.50x   n 5 vs 5  p 0.516  inside the noise"},
		},
		{
			name: "the same numbers in both arms is noise",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 2), runs(6, bench.ArmWavez, "m", 10, 2)...),
			want: []string{
				"turns    1.00x   n 6 vs 5  p 1.000  inside the noise",
				"sweeps   1.00x   n 6 vs 5  p 1.000  inside the noise",
			},
			wantNot: []string{"outside the noise"},
		},
		{
			name: "too few runs gets no verdict however large the ratio",
			recs: append(runs(4, bench.ArmBaseline, "m", 10, 4), runs(5, bench.ArmWavez, "m", 2, 0)...),
			want: []string{
				"wavez/baseline, 5 vs 4 runs",
				"turns    0.20x   n 5 vs 4  no verdict: fewer than 5 measured runs in an arm",
			},
			wantNot: []string{"the noise", "p 0"},
		},
		{
			name: "zero in both arms is noise with no ratio",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 0), runs(5, bench.ArmWavez, "m", 10, 0)...),
			want: []string{
				"sweeps      -    n 5 vs 5  p 1.000  inside the noise " +
					"(the median it is divided by is zero, so there is no ratio)",
			},
		},
		{
			name: "a zero baseline has a verdict and no ratio",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 0), runs(5, bench.ArmWavez, "m", 10, 3)...),
			want: []string{
				"sweeps      -    n 5 vs 5  p 0.008  outside the noise " +
					"(the median it is divided by is zero, so there is no ratio)",
			},
		},
		{
			name: "a harness that reports no turns has nothing to compare",
			recs: append(runs(5, bench.ArmBaseline, "m", 0, 0), runs(5, bench.ArmWavez, "m", 0, 0)...),
			want: []string{"turns       -    n 0 vs 0  no verdict: fewer than 5 measured runs in an arm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := report(t, tt.recs, "e1")
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}

			for _, not := range tt.wantNot {
				assert.NotContains(t, got, not)
			}
		})
	}
}

func TestReport_RunsThatDidNotCompleteAreCountedNotMeasured(t *testing.T) {
	t.Parallel()

	baseline := runs(5, bench.ArmBaseline, "m", 8, 1)

	tests := []struct {
		name  string
		wavez []bench.Record
		want  []string
	}{
		{
			name: "four timeouts and a failure leave nothing to divide",
			wavez: append(stopped(4, "m", bench.StopTimeout),
				stopped(1, "m", "error_max_turns")...),
			want: []string{
				"wavez           5    0/5      1       4      -         -        -       -      0       0",
				"turns       -    n 0 vs 5  no verdict: fewer than 5 measured runs in an arm",
				"wall        -    n 0 vs 5  no verdict: fewer than 5 measured runs in an arm",
			},
		},
		{
			name:  "timeouts are left out of the median and of n",
			wavez: append(runs(4, bench.ArmWavez, "m", 2, 0), stopped(3, "m", bench.StopTimeout)...),
			want: []string{
				"wavez           7    4/7      0       3      2   $0.0200       2s       0      0       0",
				"wavez/baseline, 7 vs 5 runs",
				"turns    0.25x   n 4 vs 5  no verdict: fewer than 5 measured runs in an arm",
			},
		},
		{
			name: "a run that never reached a model joins the row it failed in",
			wavez: append(runs(5, bench.ArmWavez, "m", 2, 0),
				append(stopped(2, "", bench.StopTimeout),
					stopped(1, "", bench.StopHarnessError)...)...),
			want: []string{
				"wavez           8    5/8      1       2      2   $0.0200       2s       0      0       0",
				"turns    0.25x   n 5 vs 5  p 0.008  outside the noise",
			},
		},
		{
			name: "a failed run's spending is not the price of the work",
			wavez: append(runs(5, bench.ArmWavez, "m", 8, 1),
				each(stopped(3, "m", "error_max_turns"), func(r *bench.Record) { r.Turns = 90 })...),
			want: []string{
				"wavez           8    5/8      3       0      8   $0.0800       8s       1      0       0",
				"turns    1.00x   n 5 vs 5  p 1.000  inside the noise",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := report(t, append(slicesClone(baseline), tt.wavez...), "e1")
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}

			assert.Equal(t, 1, strings.Count(got, "setup: "), "the runs that died are not a setup of their own")
		})
	}
}

func slicesClone(recs []bench.Record) []bench.Record {
	return append([]bench.Record(nil), recs...)
}

func TestReport_RefusesUnlikeArms(t *testing.T) {
	t.Parallel()

	wavez := func(edit func(r *bench.Record)) []bench.Record {
		return each(runs(5, bench.ArmWavez, "m", 4, 0), edit)
	}
	stamped := each(runs(5, bench.ArmBaseline, "m", 10, 4), func(r *bench.Record) {
		r.Build, r.Flags = "aaaa", "timeout=15m0s"
	})

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
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 4), wavez(func(r *bench.Record) { r.Harness = "pi" })...),
			want: "no comparison: baseline ran under claude and wavez under pi",
		},
		{
			name: "the task changed",
			recs: append(runs(5, bench.ArmBaseline, "m", 10, 4),
				wavez(func(r *bench.Record) { r.TaskHash = "hash2" })...),
			want: "no comparison: the task changed between baseline and wavez (hash1 then hash2)",
		},
		{
			name: "different wavez builds",
			recs: append(slicesClone(stamped),
				wavez(func(r *bench.Record) { r.Build, r.Flags = "bbbb", "timeout=15m0s" })...),
			want: "no comparison: baseline ran with wavez build aaaa and wavez with build bbbb",
		},
		{
			name: "records from before builds were stamped stand apart from stamped ones",
			recs: append(slicesClone(stamped), wavez(func(*bench.Record) {})...),
			want: "no comparison: baseline ran with wavez build aaaa and wavez with build unknown",
		},
		{
			name: "different flags",
			recs: append(slicesClone(stamped),
				wavez(func(r *bench.Record) { r.Build, r.Flags = "aaaa", "timeout=15m0s user-settings" })...),
			want: "no comparison: baseline ran with flags timeout=15m0s and wavez with flags " +
				"timeout=15m0s user-settings",
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
			assert.NotContains(t, got, "wavez/baseline")
			assert.NotContains(t, got, "x  ")
		})
	}
}

func TestReport_BuildsAndFlagsAreNeverPooled(t *testing.T) {
	t.Parallel()

	stamp := func(recs []bench.Record, build, flags string) []bench.Record {
		return each(recs, func(r *bench.Record) { r.Build, r.Flags = build, flags })
	}

	recs := slicesClone(runs(2, bench.ArmWavez, "m", 9, 0))
	recs = append(recs, stamp(runs(3, bench.ArmWavez, "m", 4, 0), "aaaa", "timeout=15m0s")...)
	recs = append(recs, stamp(runs(1, bench.ArmWavez, "m", 2, 0), "bbbb", "timeout=15m0s")...)
	recs = append(recs, stamp(runs(1, bench.ArmWavez, "m", 7, 0), "bbbb", "timeout=15m0s user-settings")...)

	got := report(t, recs, "e1")

	for _, want := range []string{
		"setup: claude, served m, task hash1, build unknown, flags unknown\n",
		"setup: claude, served m, task hash1, build aaaa, flags timeout=15m0s\n",
		"setup: claude, served m, task hash1, build bbbb, flags timeout=15m0s\n",
		"setup: claude, served m, task hash1, build bbbb, flags timeout=15m0s user-settings\n",
		"wavez           2    2/2      0       0      9 ",
		"wavez           3    3/3      0       0      4 ",
		"wavez           1    1/1      0       0      2 ",
		"wavez           1    1/1      0       0      7 ",
	} {
		assert.Contains(t, got, want)
	}
}

func TestReport_ComparesOnlyTheSharedModel(t *testing.T) {
	t.Parallel()

	recs := append(runs(5, bench.ArmBaseline, "model-a", 10, 4), runs(5, bench.ArmWavez, "model-a", 4, 0)...)
	recs = append(recs, runs(2, bench.ArmWavez, "model-b", 1, 0)...)

	got := report(t, recs, "e1")
	assert.Equal(t, 1, strings.Count(got, "wavez/baseline"))
	assert.Contains(t, got, "setup: claude, served model-a, task hash1, build unknown, flags unknown\n")
	assert.Contains(t, got, "wavez/baseline, 5 vs 5 runs")
	assert.Contains(t, got, "turns    0.40x   n 5 vs 5  p 0.008  outside the noise")
}

func TestReport_ComparesEveryArmToTheOneNamed(t *testing.T) {
	t.Parallel()

	recs := append(runs(5, bench.ArmWavezRoute, "m", 2, 0), runs(5, bench.ArmBaseline, "m", 10, 4)...)
	recs = append(recs, runs(5, bench.ArmWavez, "m", 4, 0)...)
	recs = append(recs, runs(5, bench.ArmWavezQuiet, "m", 5, 0)...)

	tests := []struct {
		name    string
		against bench.Arm
		wantNot string
		// want is the comparison blocks in order, each with its turns line.
		want [][2]string
	}{
		{
			name: "the baseline when no arm is named",
			want: [][2]string{
				{"wavez/baseline, 5 vs 5 runs", "turns    0.40x "},
				{"wavez-quiet/baseline, 5 vs 5 runs", "turns    0.50x "},
				{"wavez-route/baseline, 5 vs 5 runs", "turns    0.20x "},
			},
			wantNot: "/wavez,",
		},
		{
			name: "a wired arm", against: bench.ArmWavez,
			want: [][2]string{
				{"baseline/wavez, 5 vs 5 runs", "turns    2.50x "},
				{"wavez-quiet/wavez, 5 vs 5 runs", "turns    1.25x "},
				{"wavez-route/wavez, 5 vs 5 runs", "turns    0.50x "},
			},
			wantNot: "/baseline,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := reportAgainst(t, recs, bench.ReportOptions{Task: "e1", Against: tt.against})
			assert.NotContains(t, got, tt.wantNot)

			at := 0

			for _, block := range tt.want {
				next := strings.Index(got[at:], block[0])
				require.GreaterOrEqual(t, next, 0, "%s is missing or out of order", block[0])

				at += next
				assert.True(t, strings.HasPrefix(got[at+len(block[0])+1:], "  "+block[1]), got[at:])
			}
		})
	}

	t.Run("one arm that cannot be compared does not silence the other", func(t *testing.T) {
		t.Parallel()

		mixed := append(runs(5, bench.ArmBaseline, "m", 10, 4), runs(5, bench.ArmWavez, "m", 4, 0)...)
		mixed = append(mixed, runs(5, bench.ArmWavezRoute, "other", 2, 0)...)

		got := report(t, mixed, "e1")
		assert.Contains(t, got, "wavez/baseline, 5 vs 5 runs")
		assert.Contains(t, got, "no comparison: baseline was served m and wavez-route was served other")
	})

	t.Run("the arm compared against has no runs", func(t *testing.T) {
		t.Parallel()

		wired := append(runs(5, bench.ArmWavez, "m", 4, 0), runs(5, bench.ArmWavezRoute, "m", 2, 0)...)

		got := report(t, wired, "e1")
		assert.Contains(t, got, "no comparison: the baseline arm has no runs, and every ratio is against it")
		assert.NotContains(t, got, "/baseline,")

		got = reportAgainst(t, wired, bench.ReportOptions{Against: bench.ArmWavezQuiet})
		assert.Contains(t, got, "no comparison: the wavez-quiet arm has no runs")
	})
}

// onTask makes one run an arm of a task, with the arms' turns given.
func onTask(task string, baseline, wavez int) []bench.Record {
	recs := append(runs(1, bench.ArmBaseline, "m", baseline, 2), runs(1, bench.ArmWavez, "m", wavez, 1)...)

	return each(recs, func(r *bench.Record) { r.TaskID = task })
}

// fifthCheaper is seven tasks whose costs differ fortyfold, each a fifth
// cheaper with wavez: no one task could show it, and together they do.
func fifthCheaper() []bench.Record {
	sizes := []int{5, 10, 20, 40, 50, 100, 200}
	recs := make([]bench.Record, 0, 2*len(sizes))

	for i, turns := range sizes {
		recs = append(recs, onTask("t"+strconv.Itoa(i), turns, turns*4/5)...)
	}

	return recs
}

// across returns the summary across tasks at the end of a report.
func across(t *testing.T, recs []bench.Record) string {
	t.Helper()

	got := report(t, recs, "")
	_, summary, found := strings.Cut(got, "\nacross tasks, ")
	require.True(t, found, got)

	return summary
}

func TestReport_AcrossTasks(t *testing.T) {
	t.Parallel()

	cheaper := fifthCheaper()

	tests := []struct {
		name    string
		recs    []bench.Record
		want    []string
		wantNot []string
	}{
		{
			name: "every task a fifth cheaper is a result",
			recs: cheaper,
			want: []string{
				"wavez/baseline: claude, served m, build unknown, flags unknown\n",
				"  task            turns      cost     wall  sweeps\n",
				"  t0              0.80x     0.80x    0.80x      -1\n",
				"  t6              0.80x     0.80x    0.80x      -1\n",
				"  pass: wavez 7/7 runs, baseline 7/7 runs\n",
				"  turns    0.80x  n 7 tasks  p 0.016  outside the noise\n",
				"  cost     0.80x  n 7 tasks  p 0.016  outside the noise\n",
				"  sweeps   -1.00  n 7 tasks  p 0.016  outside the noise\n",
			},
			wantNot: []string{"not a win", "no verdict"},
		},
		{
			name: "five tasks get no verdict",
			recs: cheaper[:10],
			want: []string{"  turns    0.80x  n 5 tasks  no verdict: fewer than 6 paired tasks\n"},
			wantNot: []string{
				"the noise",
			},
		},
		{
			name: "tasks that move both ways are noise",
			recs: append(slicesClone(cheaper[:6]),
				append(onTask("u1", 10, 14), append(onTask("u2", 10, 13), onTask("u3", 10, 12)...)...)...),
			want: []string{"  turns    1.02x  n 6 tasks  p 0.906  inside the noise\n"},
		},
		{
			name: "spending less while passing less is not a win",
			recs: each(slicesClone(cheaper), func(r *bench.Record) {
				if r.Arm == bench.ArmWavez && r.TaskID == "t3" {
					r.Pass = false
				}
			}),
			want: []string{
				"  pass: wavez 6/7 runs, baseline 7/7 runs\n",
				"  turns    0.80x  n 7 tasks  p 0.016  outside the noise, " +
					"but not a win: wavez passed fewer of its runs\n",
			},
		},
		{
			name: "the caveat falls on whichever arm spent less",
			recs: each(slicesClone(cheaper), func(r *bench.Record) {
				if r.Arm == bench.ArmBaseline {
					r.Turns, r.Pass = r.Turns/2, r.TaskID != "t3"
				}
			}),
			want: []string{
				"  turns    1.65x  n 7 tasks  p 0.016  outside the noise, " +
					"but not a win: baseline passed fewer of its runs\n",
			},
		},
		{
			name: "a task only one arm completed is left out",
			recs: append(slicesClone(cheaper), each(
				append(runs(1, bench.ArmBaseline, "m", 9, 0), stopped(1, "m", bench.StopTimeout)...),
				func(r *bench.Record) { r.TaskID = "solo" },
			)...),
			want:    []string{"  turns    0.80x  n 7 tasks  p 0.016  outside the noise\n"},
			wantNot: []string{"  solo "},
		},
		{
			name: "a task run under another build is left out of this build's summary",
			recs: append(slicesClone(cheaper),
				each(onTask("other", 10, 1), func(r *bench.Record) { r.Build = "bbbb" })...),
			want: []string{
				"  turns    0.80x  n 7 tasks  p 0.016  outside the noise\n",
				"wavez/baseline: claude, served m, build bbbb, flags unknown\n",
				"  turns    0.10x  n 1 tasks  no verdict: fewer than 6 paired tasks\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := across(t, tt.recs)
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}

			for _, not := range tt.wantNot {
				assert.NotContains(t, got, not)
			}
		})
	}
}

func TestReport_AcrossTasksIsLeftOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		task string
		recs []bench.Record
	}{
		{name: "from a report of one task", recs: fifthCheaper(), task: "t0"},
		{name: "when one arm has nothing to pair with", recs: runs(5, bench.ArmBaseline, "m", 10, 4)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NotContains(t, report(t, tt.recs, tt.task), "across tasks")
		})
	}
}

func TestReport_Table(t *testing.T) {
	t.Parallel()

	recs := runs(3, bench.ArmBaseline, "claude-example-main", 4, 2)
	recs[0].Turns, recs[0].CostUSD, recs[0].WallMS = 2, 0.02, 2500
	recs[1].Pass = false
	recs[2].Stop, recs[2].Turns, recs[2].Pass = bench.StopHarnessError, 900, false

	bare := bench.Record{
		TaskID: "q1", TaskHash: "hash9", Arm: bench.ArmWavezRoute, Harness: "pi", WallMS: 61000,
		Stop: bench.StopCompleted, Build: "0123456789ab", Flags: `timeout=15m0s harness-cmd="pi -p {prompt}"`,
	}
	hinted := bare
	hinted.RouteHints, hinted.RenameDenials = 3, 1
	recs = append(recs, bare, hinted)

	header := "arm          runs   pass failed timeout  turns      cost     wall  sweeps  hints denials"

	want := strings.Join([]string{
		"task e1",
		"setup: claude, served claude-example-main, task hash1, build unknown, flags unknown",
		header,
		"baseline        3    1/3      1       0      3   $0.0300     3.3s       2      0       0",
		"no comparison: only one arm has runs",
		"",
		"task q1",
		`setup: pi, served -, task hash9, build 0123456789ab, flags timeout=15m0s harness-cmd="pi -p {prompt}"`,
		header,
		"wavez-route     2    0/2      0       0      -         -     1m1s       -      3       1",
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

	require.ErrorIs(t, bench.Report(failingWriter{}, nil, bench.ReportOptions{}), errWrite)
}
