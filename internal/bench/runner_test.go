package bench_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

const fixtureSession = "11111111-2222-3333-4444-555555555555"

var errBroken = errors.New("harness would not start\nsecond line")

// editor is a harness that makes the change the task asks for.
func editor(outcome bench.Outcome) *fakeHarness {
	return &fakeHarness{run: func(_ context.Context, req bench.Request) (bench.Outcome, error) {
		if err := os.WriteFile(filepath.Join(req.Dir, "README.md"), []byte("Status: done.\n"), 0o600); err != nil {
			return bench.Outcome{}, fmt.Errorf("editing the workspace: %w", err)
		}

		return outcome, nil
	}}
}

func newTask(t *testing.T) bench.Task {
	t.Helper()

	repo, first := newRepo(t)

	return bench.Task{
		ID: "t1", Prompt: "Mark it done.", Repo: repo, Rev: first,
		Checks: mustChecks(t, "README.md:Status: done.", "answer:Done"),
	}
}

// transcripts lays the fixture transcript out the way the harness does:
// one folder per project under the root.
func transcripts(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	write(t, filepath.Join(root, "-work-project", fixtureSession+".jsonl"),
		read(t, filepath.Join("..", "transcript", "testdata", "session.jsonl")))

	return root
}

func TestRunner_RecordsACompletedRun(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	temp := t.TempDir()
	harness := editor(bench.Outcome{
		Answer: "Done.", SessionID: fixtureSession, Served: "claude-example-main", Stop: bench.StopCompleted,
		Usage: bench.Usage{Input: 1, Output: 2, CacheRead: 3, CacheCreation: 4},
		Turns: 3, CostUSD: 0.01, Duration: 2 * time.Second,
	})
	runner := &bench.Runner{
		Harness: harness, HarnessName: "fake", WavezVersion: "v9", TranscriptRoot: transcripts(t),
		TempDir: temp, Env: []string{"PATH=/usr/bin:/bin"}, Timeout: time.Minute,
		Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600)) },
	}

	rec, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "haiku")
	require.NoError(t, err)

	require.NotNil(t, rec.Transcript)
	assert.Equal(t, 1, rec.Transcript.Sweeps)
	assert.GreaterOrEqual(t, rec.WallMS, int64(0))

	rec.Transcript, rec.WallMS = nil, 0
	assert.Equal(t, bench.Record{
		TaskID: "t1", TaskHash: task.Hash(), Arm: bench.ArmBaseline, Harness: "fake", Model: "haiku",
		Served: "claude-example-main", Rev: task.Rev, WavezVersion: "v9", Started: "2026-01-02T02:04:05Z",
		Stop: bench.StopCompleted, SessionID: fixtureSession,
		Checks: []bench.CheckResult{
			{Check: "README.md:Status: done.", Pass: true}, {Check: "answer:Done", Pass: true},
		},
		Usage:   bench.Usage{Input: 1, Output: 2, CacheRead: 3, CacheCreation: 4},
		Turns:   3,
		CostUSD: 0.01, HarnessMS: 2000, Pass: true,
	}, rec)

	require.Len(t, harness.reqs, 1)

	req := harness.reqs[0]
	assert.Equal(t, "Mark it done.", req.Prompt)
	assert.Equal(t, "haiku", req.Model)
	assert.Equal(t, []string{"PATH=/usr/bin:/bin"}, req.Env)
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, req.SessionID)
	assert.NoDirExists(t, req.Dir, "the workspace is removed after the run")

	left, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, left)
	assert.NotContains(t, gitIn(t, task.Repo, "worktree", "list"), "wavez-bench-")
}

func TestRunner_StopReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		run       func(ctx context.Context, req bench.Request) (bench.Outcome, error)
		name      string
		wantStop  string
		wantError string
		timeout   time.Duration
	}{
		{
			name: "a harness that says nothing about stopping completed",
			run: func(context.Context, bench.Request) (bench.Outcome, error) {
				return bench.Outcome{Answer: "Done"}, nil
			},
			wantStop: bench.StopCompleted,
		},
		{
			name: "the harness's own word for a failure is kept",
			run: func(context.Context, bench.Request) (bench.Outcome, error) {
				return bench.Outcome{Answer: "Done", Stop: "error_max_turns"}, nil
			},
			wantStop: "error_max_turns",
		},
		{
			name: "a run past the deadline is a timeout whatever the harness says",
			run: func(ctx context.Context, _ bench.Request) (bench.Outcome, error) {
				<-ctx.Done()

				return bench.Outcome{}, ctx.Err()
			},
			timeout: 20 * time.Millisecond, wantStop: bench.StopTimeout,
		},
		{
			name:     "a harness that cannot run",
			run:      func(context.Context, bench.Request) (bench.Outcome, error) { return bench.Outcome{}, errBroken },
			wantStop: bench.StopHarnessError, wantError: "running the harness: harness would not start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTask(t)
			runner := &bench.Runner{Harness: &fakeHarness{run: tt.run}, TempDir: t.TempDir(), Timeout: tt.timeout}

			rec, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
			require.NoError(t, err)

			assert.Equal(t, tt.wantStop, rec.Stop)
			assert.Equal(t, tt.wantError, rec.Error)
			assert.False(t, rec.Pass, "the file check fails, and a run that did not complete never passes")
			assert.Len(t, rec.Checks, 2)
			assert.NotEmpty(t, rec.Started)
		})
	}
}

func TestRunner_ChecksHoldingOnAFailedRunIsNotAPass(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	runner := &bench.Runner{
		Harness: editor(bench.Outcome{Answer: "Done", Stop: "error_max_turns"}), TempDir: t.TempDir(),
	}

	rec, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
	require.NoError(t, err)
	assert.True(t, bench.Passed(rec.Checks))
	assert.False(t, rec.Pass)
}

func TestRunner_KeepLeavesTheWorkspace(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	runner := &bench.Runner{Harness: editor(bench.Outcome{Answer: "Done"}), TempDir: t.TempDir(), Keep: true}

	rec, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
	require.NoError(t, err)
	assert.True(t, rec.Pass)
	assert.Equal(t, "Status: done.\n", read(t, filepath.Join(rec.Workspace, "README.md")))

	gitIn(t, task.Repo, "worktree", "remove", "--force", rec.Workspace)
}

func TestRunner_MissingTranscriptIsNotedNotFatal(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	runner := &bench.Runner{
		Harness: editor(bench.Outcome{Answer: "Done", SessionID: "no-such-session"}),
		TempDir: t.TempDir(), TranscriptRoot: t.TempDir(),
	}

	rec, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
	require.NoError(t, err)
	assert.True(t, rec.Pass)
	assert.Nil(t, rec.Transcript)
	assert.Contains(t, rec.Error, "no transcript for session no-such-session")
}

func TestRunner_WavezArmWiresTheWorkspace(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	wavez := script(t, "wavez", `echo "$@" > "$0.args"
mkdir -p .claude && echo '{"hooks":"wavez hook"}' > .claude/settings.json
`)

	var wired string

	harness := &fakeHarness{run: func(_ context.Context, req bench.Request) (bench.Outcome, error) {
		body, err := os.ReadFile(filepath.Join(req.Dir, ".claude", "settings.json"))
		if err != nil {
			return bench.Outcome{}, fmt.Errorf("reading the wiring: %w", err)
		}

		wired = string(body)

		return bench.Outcome{Answer: "Done"}, nil
	}}
	runner := &bench.Runner{
		Harness: harness, WavezBin: wavez, TempDir: t.TempDir(),
		Env: []string{"HOME=/nowhere", "PATH=/usr/bin:/bin"},
	}

	rec, err := runner.Run(t.Context(), &task, bench.ArmWavez, "")
	require.NoError(t, err)
	assert.Equal(t, bench.ArmWavez, rec.Arm)
	assert.Contains(t, wired, "wavez hook")

	req := harness.reqs[0]
	assert.Equal(t, "init --bin "+wavez+" --dir "+req.Dir+"\n", read(t, wavez+".args"))
	assert.Equal(t, []string{
		"HOME=/nowhere", "PATH=" + filepath.Dir(wavez) + ":/usr/bin:/bin",
		"WAVEZ_HOOK_LOG=" + filepath.Join(filepath.Dir(req.Dir), "hook-trace.jsonl"),
	}, req.Env, "the hooks trace beside the checkout, not inside it")
	assert.NoFileExists(t, filepath.Join(req.Dir, ".wavez.toml"), "the wavez arm leaves the route hint off")
	assert.Zero(t, rec.RouteHints+rec.RenameDenials, "hooks that never fired left no trace to count")
}

// traceOf returns the hook trace file a harness was told about.
func traceOf(t *testing.T, env []string) string {
	t.Helper()

	for _, kv := range env {
		if path, ok := strings.CutPrefix(kv, "WAVEZ_HOOK_LOG="); ok {
			return path
		}
	}

	require.Fail(t, "no hook trace in the environment")

	return ""
}

func TestRunner_RouteArmTurnsTheHintOnAndCountsWhatTheHooksSaid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// config is the .wavez.toml the pinned tree already has.
		config string
		want   string
	}{
		{name: "no configuration", want: "route = true\n"},
		{
			name:   "a configuration with a table keeps it, and the key goes first",
			config: "max_rounds = 2\n\n[decide]\nmodel = \"m\"\n",
			want:   "route = true\nmax_rounds = 2\n\n[decide]\nmodel = \"m\"\n",
		},
		{
			name:   "a hint turned off is turned on",
			config: "route = false\nmax_rounds = 2\n",
			want:   "route = true\nmax_rounds = 2\n",
		},
		{
			name:   "a key of the same name inside a table is another key and stays",
			config: "max_rounds = 2\n\n[decide]\nroute = \"local\"\n",
			want:   "route = true\nmax_rounds = 2\n\n[decide]\nroute = \"local\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTask(t)

			if tt.config != "" {
				write(t, filepath.Join(task.Repo, ".wavez.toml"), tt.config)
				gitIn(t, task.Repo, "add", ".")
				gitIn(t, task.Repo, "commit", "--quiet", "-m", "configure")
				task.Rev = gitIn(t, task.Repo, "rev-parse", "HEAD")
			}

			var config string

			harness := &fakeHarness{run: func(_ context.Context, req bench.Request) (bench.Outcome, error) {
				config = read(t, filepath.Join(req.Dir, ".wavez.toml"))
				trace := strings.Join([]string{
					`{"event":"SessionStart","outcome":"silent"}`,
					`{"event":"UserPromptSubmit","outcome":"context"}`,
					`{"event":"UserPromptSubmit","outcome":"silent"}`,
					`{"event":"PreToolUse","outcome":"deny"}`,
					`{"event":"PreToolUse","outcome":"deny"}`,
					`{"event":"PostToolUse","outcome":"context"}`,
					`not a trace line`,
				}, "\n")

				return bench.Outcome{Answer: "Done"}, os.WriteFile(traceOf(t, req.Env), []byte(trace), 0o600)
			}}
			runner := &bench.Runner{
				Harness: harness, WavezBin: script(t, "wavez", "exit 0\n"), TempDir: t.TempDir(),
				Env: []string{"PATH=/usr/bin:/bin"},
			}

			rec, err := runner.Run(t.Context(), &task, bench.ArmWavezRoute, "")
			require.NoError(t, err)
			assert.Equal(t, bench.ArmWavezRoute, rec.Arm)
			assert.Equal(t, tt.want, config)
			assert.Equal(t, 1, rec.RouteHints)
			assert.Equal(t, 2, rec.RenameDenials)
			assert.Equal(t, []string{"route = true"}, rec.ArmConfig)
			assert.Empty(t, rec.ArmEnv)
		})
	}
}

func TestRunner_RouteArmThatCannotBeConfiguredIsAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		plant func(t *testing.T, repo string)
		want  string
	}{
		{
			name: "the configuration cannot be read",
			plant: func(t *testing.T, repo string) {
				t.Helper()
				write(t, filepath.Join(repo, ".wavez.toml", "inside"), "x\n")
			},
			want: "reading",
		},
		{
			name: "the configuration cannot be written",
			plant: func(t *testing.T, repo string) {
				t.Helper()
				require.NoError(t, os.Symlink(filepath.Join("missing", "x"), filepath.Join(repo, ".wavez.toml")))
			},
			want: "writing the arm's settings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTask(t)
			tt.plant(t, task.Repo)
			gitIn(t, task.Repo, "add", ".")
			gitIn(t, task.Repo, "commit", "--quiet", "-m", "plant")
			task.Rev = gitIn(t, task.Repo, "rev-parse", "HEAD")

			runner := &bench.Runner{
				Harness: editor(bench.Outcome{}), WavezBin: script(t, "wavez", "exit 0\n"), TempDir: t.TempDir(),
			}

			_, err := runner.Run(t.Context(), &task, bench.ArmWavezRoute, "")
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRunner_TaskGatesReachOnlyWiredArms(t *testing.T) {
	t.Parallel()

	const gates = "[[check]]\nname = \"py\"\nglobs = [\"*.py\"]\ncommand = \"true {files}\"\n"

	tests := []struct {
		arm bench.Arm
		// want is the .wavez.toml the harness finds.
		want string
	}{
		{arm: bench.ArmBaseline, want: "max_rounds = 2"},
		{arm: bench.ArmWavez, want: "max_rounds = 2\n" + gates},
		{arm: bench.ArmWavezRoute, want: "route = true\nmax_rounds = 2\n" + gates},
	}

	for _, tt := range tests {
		t.Run(string(tt.arm), func(t *testing.T) {
			t.Parallel()

			task := newTask(t)
			task.Gates = gates

			// The pinned tree's file ends without a newline, and the
			// gates still start on a line of their own.
			write(t, filepath.Join(task.Repo, ".wavez.toml"), "max_rounds = 2")
			gitIn(t, task.Repo, "add", ".")
			gitIn(t, task.Repo, "commit", "--quiet", "-m", "configure")
			task.Rev = gitIn(t, task.Repo, "rev-parse", "HEAD")

			var config string

			harness := &fakeHarness{run: func(_ context.Context, req bench.Request) (bench.Outcome, error) {
				config = read(t, filepath.Join(req.Dir, ".wavez.toml"))

				return bench.Outcome{Answer: "Done"}, nil
			}}
			runner := &bench.Runner{
				Harness: harness, WavezBin: script(t, "wavez", "exit 0\n"), TempDir: t.TempDir(),
				Env: []string{"PATH=/usr/bin:/bin"},
			}

			_, err := runner.Run(t.Context(), &task, tt.arm, "")
			require.NoError(t, err)
			assert.Equal(t, tt.want, config)
		})
	}
}

func TestRunner_GatesTheTreeAlreadyDeclaresAreAnError(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	task.Gates = "[[check]]\nname = \"py\"\nglobs = [\"*.py\"]\ncommand = \"true\"\n"

	write(t, filepath.Join(task.Repo, ".wavez.toml"), task.Gates)
	gitIn(t, task.Repo, "add", ".")
	gitIn(t, task.Repo, "commit", "--quiet", "-m", "configure")
	task.Rev = gitIn(t, task.Repo, "rev-parse", "HEAD")

	runner := &bench.Runner{
		Harness: editor(bench.Outcome{}), WavezBin: script(t, "wavez", "exit 0\n"), TempDir: t.TempDir(),
	}

	_, err := runner.Run(t.Context(), &task, bench.ArmWavez, "")
	require.ErrorContains(t, err, "declared twice")
}

func TestRunner_WavezArmWithoutAPathStillGetsOne(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	wavez := script(t, "wavez", "exit 0\n")
	harness := editor(bench.Outcome{Answer: "Done"})
	runner := &bench.Runner{Harness: harness, WavezBin: wavez, TempDir: t.TempDir(), Env: []string{"PATH="}}

	_, err := runner.Run(t.Context(), &task, bench.ArmWavez, "")
	require.NoError(t, err)
	assert.Equal(t, "PATH="+filepath.Dir(wavez), harness.reqs[0].Env[0])
}

func TestRunner_SetupFailuresAreErrorsNotRecords(t *testing.T) {
	t.Parallel()

	never := &fakeHarness{run: func(context.Context, bench.Request) (bench.Outcome, error) {
		return bench.Outcome{}, errBroken
	}}

	t.Run("init fails", func(t *testing.T) {
		t.Parallel()

		task := newTask(t)
		temp := t.TempDir()
		runner := &bench.Runner{
			Harness: never, TempDir: temp, WavezBin: script(t, "wavez", "echo no such command: init >&2\nexit 1\n"),
		}

		_, err := runner.Run(t.Context(), &task, bench.ArmWavez, "")
		require.ErrorContains(t, err, "no such command: init")

		left, readErr := os.ReadDir(temp)
		require.NoError(t, readErr)
		assert.Empty(t, left, "a failed setup still removes its workspace")
	})

	t.Run("unknown rev", func(t *testing.T) {
		t.Parallel()

		task := newTask(t)
		task.Rev = "0000000000000000000000000000000000000000"
		runner := &bench.Runner{Harness: never, TempDir: t.TempDir()}

		_, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
		require.Error(t, err)
	})

	t.Run("canceled while the harness runs", func(t *testing.T) {
		t.Parallel()

		task := newTask(t)
		ctx, cancel := context.WithCancel(t.Context())
		runner := &bench.Runner{TempDir: t.TempDir(), Harness: &fakeHarness{
			run: func(context.Context, bench.Request) (bench.Outcome, error) {
				cancel()

				return bench.Outcome{}, nil
			},
		}}

		_, err := runner.Run(ctx, &task, bench.ArmBaseline, "")
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, never.reqs)
	})
}

func TestRunner_BaselineRefusesAWiredWorkspace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
		want bool
	}{
		{
			name: "hooks in project settings", path: ".claude/settings.json",
			body: `{"command":"/x/wavez hook stop"}`, want: true,
		},
		{
			name: "hooks in local settings", path: ".claude/settings.local.json",
			body: `{"command":"wavez gate"}`, want: true,
		},
		{
			name: "a quoted binary", path: ".claude/settings.json",
			body: `{"command":"\"/opt/my tools/wavez\" hook"}`, want: true,
		},
		{name: "a mention that is not a hook", path: ".claude/settings.json", body: `{"note":"wavez hooks later"}`},
		{name: "a wavez skill", path: ".claude/skills/wavez-refactor/SKILL.md", body: "skill", want: true},
		{name: "unrelated settings", path: ".claude/settings.json", body: `{"model":"sonnet"}`},
		{name: "an unrelated skill", path: ".claude/skills/review/SKILL.md", body: "skill"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo, _ := newRepo(t)
			write(t, filepath.Join(repo, tt.path), tt.body)
			gitIn(t, repo, "add", "-A")
			gitIn(t, repo, "commit", "--quiet", "-m", "settings")

			task := bench.Task{ID: "t", Prompt: "p", Repo: repo, Rev: gitIn(t, repo, "rev-parse", "HEAD")}
			runner := &bench.Runner{Harness: editor(bench.Outcome{}), TempDir: t.TempDir()}

			_, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
			if !tt.want {
				require.NoError(t, err)

				return
			}

			var dirty *bench.ContaminatedError

			require.ErrorAs(t, err, &dirty)
			assert.True(t, strings.HasSuffix(filepath.Dir(dirty.Path), filepath.Dir(tt.path)) ||
				strings.Contains(dirty.Path, ".claude"), dirty.Path)
			assert.Contains(t, err.Error(), "baseline")
		})
	}
}

func TestRunner_ArmsDifferByWhatTheirDefinitionSays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arm        bench.Arm
		wantEnv    []string
		wantConfig []string
		wantWired  bool
	}{
		{arm: bench.ArmBaseline},
		{arm: bench.ArmWavez, wantWired: true},
		{arm: bench.ArmWavezQuiet, wantWired: true, wantEnv: []string{"WAVEZ_ANNOUNCE=0"}},
		{arm: bench.ArmWavezRoute, wantWired: true, wantConfig: []string{"route = true"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.arm), func(t *testing.T) {
			t.Parallel()

			task := newTask(t)
			wavez := script(t, "wavez", "touch \"$0.ran\"\n")
			harness := editor(bench.Outcome{Answer: "Done"})
			runner := &bench.Runner{
				Harness: harness, WavezBin: wavez, TempDir: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin"},
				Build: "0123456789ab", Flags: "timeout=1m0s",
			}

			rec, err := runner.Run(t.Context(), &task, tt.arm, "")
			require.NoError(t, err)

			assert.Equal(t, tt.wantEnv, rec.ArmEnv)
			assert.Equal(t, tt.wantConfig, rec.ArmConfig)
			assert.Equal(t, "0123456789ab", rec.Build)
			assert.Equal(t, "timeout=1m0s", rec.Flags)

			env := harness.reqs[0].Env
			assert.Equal(t, !tt.wantWired, slices.Equal(env, []string{"PATH=/usr/bin:/bin"}),
				"only the baseline gets the environment as it came")
			assert.Subset(t, env, tt.wantEnv, "the harness, and so every hook it runs, sees the arm's environment")
			assert.Equal(t, tt.arm == bench.ArmWavezQuiet, slices.Contains(env, "WAVEZ_ANNOUNCE=0"))

			if tt.wantWired {
				assert.FileExists(t, wavez+".ran")
			} else {
				assert.NoFileExists(t, wavez+".ran")
			}
		})
	}
}

func TestRunner_ACheckThatHangsFailsTheRunAndIsNotWaitedFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check string
	}{
		{name: "a command that never returns", check: "cmd:sleep 600"},
		{name: "inverted, it still fails", check: "!cmd:sleep 600"},
		{name: "a command whose child never returns", check: "cmd:sleep 600 & wait"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTask(t)
			task.Checks = mustChecks(t, "answer:Done", tt.check)
			runner := &bench.Runner{
				Harness: editor(bench.Outcome{Answer: "Done"}), TempDir: t.TempDir(),
				CheckTimeout: 200 * time.Millisecond,
			}

			start := time.Now()
			rec, err := runner.Run(t.Context(), &task, bench.ArmBaseline, "")
			require.NoError(t, err)
			assert.Less(t, time.Since(start), time.Minute)

			assert.Equal(t, bench.StopCompleted, rec.Stop, "the harness finished; it is the check that did not")
			assert.False(t, rec.Pass)
			assert.Equal(t, []bench.CheckResult{
				{Check: "answer:Done", Pass: true},
				{Check: tt.check, Detail: "timed out after 200ms"},
			}, rec.Checks)
		})
	}
}

func TestParseArm(t *testing.T) {
	t.Parallel()

	names := []string{"baseline", "wavez", "wavez-quiet", "wavez-route", "wavez-gates", "wavez-warm", "wavez-locate"}

	arms := make([]bench.Arm, 0, len(names))
	for _, name := range names {
		arms = append(arms, bench.Arm(name))
	}

	assert.Equal(t, arms, bench.Arms())

	for _, name := range names {
		arm, err := bench.ParseArm(name)
		require.NoError(t, err)
		assert.Equal(t, bench.Arm(name), arm)
	}

	_, err := bench.ParseArm("control")

	var unknown *bench.UnknownArmError

	require.ErrorAs(t, err, &unknown)
	assert.Contains(t, err.Error(), "control")
	assert.Contains(t, err.Error(), "baseline, wavez, wavez-quiet, wavez-route, wavez-gates, wavez-warm, wavez-locate")
}

func TestBinaryVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"prefixed", "echo 'wavez 1.2.3 (abc, today)'\n", "1.2.3 (abc, today)"},
		{"bare", "echo v2\n", "v2"},
		{"silent", "exit 0\n", "unknown"},
		{"failing", "exit 1\n", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, bench.BinaryVersion(t.Context(), script(t, "wavez", tt.body)))
		})
	}
}

func TestRunner_OverlayReachesOnlyWiredArms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arm  bench.Arm
		want bool
	}{
		{arm: bench.ArmBaseline, want: false},
		{arm: bench.ArmWavez, want: true},
	}

	for _, tt := range tests {
		t.Run(string(tt.arm), func(t *testing.T) {
			t.Parallel()

			task := newTask(t)
			overlay := t.TempDir()
			write(t, filepath.Join(overlay, ".detest", "cache", "trace.json"), "{}")

			var found bool

			harness := &fakeHarness{run: func(_ context.Context, req bench.Request) (bench.Outcome, error) {
				found = fileHasContent(filepath.Join(req.Dir, ".detest", "cache", "trace.json"))

				return bench.Outcome{Answer: "Done"}, nil
			}}
			runner := &bench.Runner{
				Harness: harness, WavezBin: script(t, "wavez", "exit 0\n"), TempDir: t.TempDir(),
				Env: []string{"PATH=/usr/bin:/bin"}, Overlay: overlay,
			}

			_, err := runner.Run(t.Context(), &task, tt.arm, "")
			require.NoError(t, err)
			assert.Equal(t, tt.want, found)
		})
	}
}

func TestRunner_OverlayOverAFileOfTheTreeIsAnError(t *testing.T) {
	t.Parallel()

	task := newTask(t)
	overlay := t.TempDir()

	entries, err := os.ReadDir(task.Repo)
	require.NoError(t, err)

	for _, entry := range entries {
		if entry.Type().IsRegular() {
			write(t, filepath.Join(overlay, entry.Name()), "replaced")
		}
	}

	runner := &bench.Runner{
		Harness: &fakeHarness{}, WavezBin: script(t, "wavez", "exit 0\n"), TempDir: t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin"}, Overlay: overlay,
	}

	_, err = runner.Run(t.Context(), &task, bench.ArmWavez, "")
	require.ErrorContains(t, err, "copying the overlay")
}

func TestTreeDigest_FollowsNamesAndContent(t *testing.T) {
	t.Parallel()

	digest := func(files map[string]string) string {
		dir := t.TempDir()
		for name, body := range files {
			write(t, filepath.Join(dir, name), body)
		}

		got, err := bench.TreeDigest(dir)
		require.NoError(t, err)

		return got
	}

	base := digest(map[string]string{"a/b": "one"})
	assert.Equal(t, base, digest(map[string]string{"a/b": "one"}))
	assert.NotEqual(t, base, digest(map[string]string{"a/b": "two"}))
	assert.NotEqual(t, base, digest(map[string]string{"a/c": "one"}))
}
