package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/scripttest"
)

// benchFixture is a repository with a tasks file beside it, the way the
// project's own bench directory sits in its repository.
type benchFixture struct {
	tasks   string
	records string
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()

	full := append([]string{"-c", "user.name=bench", "-c", "user.email=bench@example.invalid"}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...) //nolint:gosec // git, with the test's own arguments
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return strings.TrimSpace(string(out))
}

func newBenchFixture(t *testing.T) benchFixture {
	t.Helper()

	repo := t.TempDir()
	gitRun(t, repo, "init", "--quiet")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("Status: first.\n"), 0o600))
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "--quiet", "-m", "first")

	fx := benchFixture{
		tasks:   filepath.Join(repo, "bench", "tasks.toml"),
		records: filepath.Join(t.TempDir(), "records.jsonl"),
	}
	head := gitRun(t, repo, "rev-parse", "HEAD")
	body := "[[task]]\nid = 'ok'\nprompt = 'Say done.'\nrev = '" + head + "'\n" +
		"checks = ['answer:done', 'README.md:Status: first.']\n\n" +
		"[[task]]\nid = 'never'\nprompt = 'Say something else.'\nrev = '" + head + "'\n" +
		"checks = ['answer:absent']\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(fx.tasks), 0o750))
	require.NoError(t, os.WriteFile(fx.tasks, []byte(body), 0o600))

	return fx
}

func runCommand(t *testing.T, fn func(context.Context, []string, stdio) error, args ...string) (string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	err := fn(t.Context(), args, stdio{in: strings.NewReader(""), out: &out, err: &errOut})

	return out.String(), err
}

func TestBench_ListRunReport(t *testing.T) {
	t.Parallel()

	fx := newBenchFixture(t)

	list, err := runCommand(t, runBench, "list", "--tasks", fx.tasks)
	require.NoError(t, err)
	assert.Equal(t, "ok     2 check(s)  Say done.\nnever  1 check(s)  Say something else.\n", list)

	// The generic harness stands in for a model: echo is the whole agent.
	lines, err := runCommand(t, runBench, "run", "ok", "--arm", "baseline", "--n", "2", "--model", "m1",
		"--tasks", fx.tasks, "--records", fx.records, "--harness-cmd", "echo all done with {model}")
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(lines, "\n"))
	assert.Contains(t, lines,
		"ok baseline 1/2 pass stop=completed checks=2/2 turns=0 cost=$0.0000 wall=0s sweeps=- served=-")
	assert.Contains(t, lines, "ok baseline 2/2 pass")

	failed, err := runCommand(t, runBench, "run", "--arm=baseline", "--keep", "--tasks", fx.tasks,
		"--records", fx.records, "--harness-cmd", "echo no", "never")
	require.NoError(t, err)
	assert.Contains(t, failed, "never baseline 1/1 FAIL stop=completed checks=0/1")
	assert.Contains(t, failed, " workspace=")

	kept := strings.TrimSpace(failed[strings.Index(failed, " workspace=")+len(" workspace="):])
	assert.FileExists(t, filepath.Join(kept, "README.md"))
	gitRun(t, filepath.Dir(filepath.Dir(fx.tasks)), "worktree", "remove", "--force", kept)
	require.NoError(t, os.RemoveAll(filepath.Dir(kept)))

	recs, err := bench.LoadRecords(fx.records)
	require.NoError(t, err)
	require.Len(t, recs, 3)
	assert.Equal(t, "echo", recs[0].Harness)
	assert.Equal(t, "m1", recs[0].Model)
	assert.Equal(t, version, recs[0].WavezVersion)
	assert.Regexp(t, `^[0-9a-f]{12}$`, recs[0].Build)
	assert.Equal(t, `timeout=15m0s check-timeout=10m0s harness-cmd="echo all done with {model}"`, recs[0].Flags)
	assert.Zero(t, recs[0].Seed, "one arm alone has no order to seed")
	assert.True(t, recs[0].Pass)
	assert.False(t, recs[2].Pass)

	all, err := runCommand(t, runBench, "report", "--records", fx.records)
	require.NoError(t, err)
	assert.Contains(t, all, "task ok\n")
	assert.Contains(t, all, "task never\n")
	assert.Contains(t, all, "no comparison: only one arm has runs")

	one, err := runCommand(t, runBench, "report", "never", "--records", fx.records)
	require.NoError(t, err)
	assert.NotContains(t, one, "task ok\n")
}

// inertWavez is a wavez that wires nothing, for runs whose harness is not
// Claude Code and would not read the wiring.
func inertWavez(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "wavez")
	scripttest.Write(t, path, "#!/bin/sh\nexit 0\n")

	return path
}

func TestBench_SeveralArmsAreInterleaved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		wantArms  []string
		wantTasks []string
	}{
		{
			name: "arms separated by commas", args: []string{"ok", "--arm", "baseline,wavez,wavez-quiet,wavez-route"},
			wantArms: []string{"baseline", "wavez", "wavez-quiet", "wavez-route"}, wantTasks: []string{"ok"},
		},
		{
			name: "the flag given twice", args: []string{"ok", "--arm", "wavez", "--arm", "baseline"},
			wantArms: []string{"baseline", "wavez"}, wantTasks: []string{"ok"},
		},
		{
			name: "several tasks", args: []string{"never,ok", "--arm", "baseline,wavez"},
			wantArms: []string{"baseline", "wavez"}, wantTasks: []string{"never", "ok"},
		},
		{
			name: "every task", args: []string{"--all", "--arm", "baseline,wavez-quiet"},
			wantArms: []string{"baseline", "wavez-quiet"}, wantTasks: []string{"ok", "never"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const reps = 6

			fx := newBenchFixture(t)
			wavez := inertWavez(t)

			args := append([]string{"run"}, tt.args...)
			args = append(args, "--n", "6", "--seed", "42", "--tasks", fx.tasks, "--records", fx.records,
				"--wavez-bin", wavez, "--harness-cmd", "echo done")

			out, err := runCommand(t, runBench, args...)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(out, "seed 42: each repetition runs "), out)
			assert.Contains(t, out, " in a freshly shuffled order\n")

			recs, err := bench.LoadRecords(fx.records)
			require.NoError(t, err)
			require.Len(t, recs, reps*len(tt.wantArms)*len(tt.wantTasks))

			var orders []string

			for block := 0; block < len(recs); block += len(tt.wantArms) {
				var arms []string

				for slot, rec := range recs[block : block+len(tt.wantArms)] {
					each := block / len(tt.wantArms)

					assert.Equal(t, tt.wantTasks[each%len(tt.wantTasks)], rec.TaskID, "a block is one task")
					assert.Equal(t, each/len(tt.wantTasks)+1, rec.Rep, "every task runs before the next repetition")
					assert.Equal(t, slot+1, rec.Slot)
					assert.Equal(t, uint64(42), rec.Seed)

					arms = append(arms, string(rec.Arm))
				}

				assert.ElementsMatch(t, tt.wantArms, arms, "a block runs every arm once")

				orders = append(orders, strings.Join(arms, ","))
			}

			slices.Sort(orders)
			assert.Greater(t, len(slices.Compact(orders)), 1, "the arms do not always run in the same order")

			again := filepath.Join(t.TempDir(), "again.jsonl")
			args[slices.Index(args, "--records")+1] = again

			_, err = runCommand(t, runBench, args...)
			require.NoError(t, err)

			repeat, err := bench.LoadRecords(again)
			require.NoError(t, err)
			require.Len(t, repeat, len(recs))

			for i := range recs {
				assert.Equal(t, recs[i].Arm, repeat[i].Arm, "the seed gives the same order again")
			}
		})
	}
}

func TestBench_PicksASeedWhenNoneIsGiven(t *testing.T) {
	t.Parallel()

	fx := newBenchFixture(t)

	out, err := runCommand(t, runBench, "run", "ok", "--arm", "baseline,wavez", "--tasks", fx.tasks,
		"--records", fx.records, "--wavez-bin", inertWavez(t), "--harness-cmd", "echo done")
	require.NoError(t, err)
	require.Regexp(t, `^seed [1-9][0-9]*: `, out)

	recs, err := bench.LoadRecords(fx.records)
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "seed "+strconv.FormatUint(recs[0].Seed, 10)+":", strings.Fields(out)[0]+" "+strings.Fields(out)[1])
	assert.Equal(t, recs[0].Seed, recs[1].Seed)
}

func TestBench_FlagsThatChangeARunAreRecorded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		args []string
	}{
		{name: "the defaults", want: `timeout=15m0s check-timeout=10m0s harness-cmd="echo done"`},
		{
			name: "other bounds", args: []string{"--timeout", "2m", "--check-timeout", "30s"},
			want: `timeout=2m0s check-timeout=30s harness-cmd="echo done"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fx := newBenchFixture(t)
			args := append([]string{
				"run", "ok", "--arm", "baseline", "--tasks", fx.tasks, "--records", fx.records,
				"--harness-cmd", "echo done",
			}, tt.args...)

			_, err := runCommand(t, runBench, args...)
			require.NoError(t, err)

			recs, err := bench.LoadRecords(fx.records)
			require.NoError(t, err)
			require.Len(t, recs, 1)
			assert.Equal(t, tt.want, recs[0].Flags)
		})
	}

	t.Run("user settings under Claude Code", func(t *testing.T) {
		t.Parallel()

		plain := runOptions{timeout: time.Minute, checkTimeout: time.Second}
		loaded := plain
		loaded.userSettings = true

		assert.Equal(t, "timeout=1m0s check-timeout=1s", plain.flags())
		assert.Equal(t, "timeout=1m0s check-timeout=1s user-settings", loaded.flags())
	})
}

func TestBench_ReportAgainstAnotherArm(t *testing.T) {
	t.Parallel()

	fx := newBenchFixture(t)

	_, err := runCommand(t, runBench, "run", "ok", "--arm", "baseline,wavez,wavez-quiet", "--tasks", fx.tasks,
		"--records", fx.records, "--wavez-bin", inertWavez(t), "--harness-cmd", "echo done")
	require.NoError(t, err)

	tests := []struct {
		name    string
		wantNot string
		args    []string
		want    []string
	}{
		{
			name: "the baseline by default", wantNot: "/wavez,",
			want: []string{"wavez/baseline,", "wavez-quiet/baseline,"},
		},
		{
			name: "a wired arm", args: []string{"--against", "wavez"},
			want: []string{"baseline/wavez,", "wavez-quiet/wavez,"}, wantNot: "/baseline,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"report", "ok", "--records", fx.records}, tt.args...)

			out, err := runCommand(t, runBench, args...)
			require.NoError(t, err)

			for _, want := range tt.want {
				assert.Contains(t, out, want)
			}

			assert.NotContains(t, out, tt.wantNot)
		})
	}
}

func TestBench_WavezArmUsesTheNamedBinary(t *testing.T) {
	t.Parallel()

	fx := newBenchFixture(t)
	wavez := filepath.Join(t.TempDir(), "wavez")
	body := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'wavez 9.9.9 (abc, today)'; exit 0; fi\n" +
		"mkdir -p .claude && echo \"$@\" > .claude/wired\n"

	scripttest.Write(t, wavez, body)

	// The harness reports whether init ran and whether wavez resolves on PATH.
	line, err := runCommand(t, runBench, "run", "ok", "--arm", "wavez", "--tasks", fx.tasks, "--records", fx.records,
		"--wavez-bin", wavez, "--harness-cmd", `sh -c "cat .claude/wired; command -v wavez; echo done"`)
	require.NoError(t, err)
	assert.Contains(t, line, "ok wavez 1/1 pass")

	recs, err := bench.LoadRecords(fx.records)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "9.9.9 (abc, today)", recs[0].WavezVersion)
	assert.Equal(t, bench.ArmWavez, recs[0].Arm)
}

func TestBench_UsageErrors(t *testing.T) {
	t.Parallel()

	fx := newBenchFixture(t)

	tests := []struct {
		name string
		want string
		args []string
	}{
		{name: "no subcommand", args: nil, want: "usage:"},
		{name: "unknown subcommand", args: []string{"race"}, want: `unknown bench command "race"`},
		{name: "run without a task", args: []string{"run", "--arm", "baseline"}, want: "needs a task id"},
		{name: "run without an arm", args: []string{"run", "ok"}, want: "needs --arm"},
		{name: "run with an unknown arm", args: []string{"run", "ok", "--arm", "control"}, want: `unknown arm`},
		{name: "run with an unknown arm listed", args: []string{"run", "ok", "--arm=wavez,x"}, want: `unknown arm "x"`},
		{name: "run with an arm named twice", args: []string{"run", "ok", "--arm", "wavez,wavez"}, want: "named twice"},
		{name: "run with a task and every task", args: []string{"run", "ok", "--all", "--arm=wavez"}, want: "both"},
		{
			name: "run with a task named twice", want: "task ok is named twice",
			args: []string{"run", "ok,ok", "--arm", "baseline", "--tasks", fx.tasks},
		},
		{
			name: "run with a wavez binary that is not there", want: "identifying the build",
			args: []string{"run", "ok", "--arm", "wavez", "--tasks", fx.tasks, "--wavez-bin", "/absent/wavez"},
		},
		{name: "report against an unknown arm", args: []string{"report", "--against", "control"}, want: "unknown arm"},
		{name: "run with n below one", args: []string{"run", "ok", "--arm=baseline", "--n=0"}, want: "--n must be"},
		{name: "run with two tasks", args: []string{"run", "ok", "--arm=baseline", "never"}, want: "unexpected"},
		{name: "run with an unknown flag", args: []string{"run", "ok", "--fast"}, want: "parsing flags"},
		{
			name: "run with an unknown task", want: `no task with id "h99"`,
			args: []string{"run", "h99", "--arm", "baseline", "--tasks", fx.tasks},
		},
		{
			name: "run with an empty harness command", want: "harness command is empty",
			args: []string{"run", "ok", "--arm", "baseline", "--tasks", fx.tasks, "--harness-cmd", " "},
		},
		{name: "run with missing tasks", args: []string{"run", "ok", "--arm=baseline", "--tasks=/x"}, want: "loading"},
		{name: "list with missing tasks", args: []string{"list", "--tasks", "/absent.toml"}, want: "loading tasks"},
		{name: "list with an argument", args: []string{"list", "a", "b"}, want: "unexpected argument"},
		{name: "report over a bad file", args: []string{"report", "--records", fx.tasks}, want: "records line 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := runCommand(t, runBench, tt.args...)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestBench_ReportOfNoRecords(t *testing.T) {
	t.Parallel()

	out, err := runCommand(t, runBench, "report", "e1", "--records", filepath.Join(t.TempDir(), "absent.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "no runs recorded for task e1\n", out)
}

func TestStats(t *testing.T) {
	t.Parallel()

	fixture := filepath.Join("..", "..", "internal", "transcript", "testdata", "session.jsonl")

	out, err := runCommand(t, runStats, fixture)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "session       "+fixture+"\nturns         11 (1 subagent"), out)
	assert.Contains(t, out, "sweeps        2 (go test 1, pytest 1)")

	tests := []struct {
		name string
		want string
		args []string
	}{
		{name: "no argument", args: nil, want: "usage: wavez stats"},
		{name: "a flag", args: []string{"--help"}, want: "usage: wavez stats"},
		{name: "two arguments", args: []string{"a", "b"}, want: "usage: wavez stats"},
		{name: "an unknown session", args: []string{"00000000-0000-0000-0000-00000000dead"}, want: "no transcript for"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := runCommand(t, runStats, tt.args...)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestBench_RefusesAWiredArmUnderAnotherBinaryName(t *testing.T) {
	t.Parallel()

	fx := newBenchFixture(t)

	// The test binary is not called wavez, and it is what a run falls back to.
	_, err := runCommand(t, runBench, "run", "ok", "--arm", "baseline,wavez", "--tasks", fx.tasks,
		"--records", fx.records, "--harness-cmd", "echo done")
	require.ErrorContains(t, err, "named wavez")
}
