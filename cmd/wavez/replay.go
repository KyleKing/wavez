package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/replay"
	"github.com/kyleking/wavez/internal/vcs"
)

// replayRun runs one task of the fixed set in a throwaway jj workspace and
// appends what it spent to the records file. The workspace is what makes the
// run repeatable: the task's prompt names files it edits, so a second run in
// the live tree would start from the first run's changes and measure
// something else.
func replayRun(ctx context.Context, root string, opt options) error {
	tasks, err := replay.LoadTasks(filepath.Join(root, replay.DefaultTasksPath))
	if err != nil {
		return err //nolint:wrapcheck // LoadTasks already names the file and the failure
	}

	task, err := replay.Find(tasks, opt.replay)
	if err != nil {
		return err //nolint:wrapcheck // Find already lists the ids it has
	}

	dir := filepath.Join(scratchBase(), "wavez-replay-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	name := filepath.Base(dir)
	jj := vcs.NewJj()

	if err := jj.AddWorkspace(ctx, root, name, dir); err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	if err := seedDerivedState(root, dir); err != nil {
		return err
	}

	defer func() {
		_ = jj.ForgetWorkspace(ctx, root, name) //nolint:errcheck // cleanup
		_ = os.RemoveAll(dir)                   //nolint:errcheck // cleanup
	}()

	run := replay.Run{
		Task:     task.ID,
		Label:    replayLabel(ctx, root, opt),
		Model:    opt.model,
		TaskHash: task.Hash(),
		MaxTurns: opt.maxTurns,
	}
	fmt.Fprintf(os.Stderr, "replay %s as %s in %s\n", run.Task, run.Label, dir)

	started := time.Now()

	sub := opt
	sub.replay = ""
	sub.dir = dir
	sub.prompt = task.Prompt

	info, runErr := headlessRun(ctx, sub)
	if info.ID == "" {
		return runErr
	}

	checks := replay.Verify(ctx, task, dir, info.Text)

	if err := keepLog(root, dir, string(info.ID)); err != nil {
		return err
	}

	stats, err := summarizeLog(root, string(info.ID))
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	rec := replay.NewRecord(run, started, stopReason(string(info.Outcome.Stop), runErr), stats, checks)
	if err := replay.Append(filepath.Join(root, replay.DefaultRecordsPath), rec); err != nil {
		return err //nolint:wrapcheck // Append already names the file and the failure
	}

	fmt.Fprintf(os.Stderr, "recorded %s/%s: %s in %d turns (%s), checks %s\n",
		rec.Task, rec.Label, rec.Stop, rec.Stats.Turns,
		replay.TierMix(rec.Stats.TierTurns), rec.CheckSummary())

	for _, c := range rec.Checks {
		if !c.Pass {
			fmt.Fprintf(os.Stderr, "  failed: %s (%s)\n", c.Check, c.Note)
		}
	}

	return runErr
}

// keepLog copies the run's thread log out of the workspace before the
// workspace goes away. The counters live on the record, and the transcript
// they came from is what a surprising counter is diagnosed with, so it lands
// in the project's own log directory where -stats reads it by thread id.
func keepLog(root, dir, id string) error {
	src := filepath.Join(app.ThreadLogDir(dir), id+".jsonl")

	body, err := os.ReadFile(src) //nolint:gosec // the path is built from a thread this process just ran
	if err != nil {
		return fmt.Errorf("replay: reading %s: %w", src, err)
	}

	dst := app.ThreadLogDir(root)
	if err := os.MkdirAll(dst, logDirMode); err != nil {
		return fmt.Errorf("replay: creating %s: %w", dst, err)
	}

	//nolint:gosec // id is a thread id this process generated, not input
	if err := os.WriteFile(filepath.Join(dst, id+".jsonl"), body, logFileMode); err != nil {
		return fmt.Errorf("replay: writing the kept log: %w", err)
	}

	return nil
}

const (
	logDirMode  = 0o750
	logFileMode = 0o600
)

// seedDerivedState copies the project's code-intelligence store and coverage
// manifest into the workspace. Both are ignored by version control, so a
// fresh workspace has neither and rebuilds them from the same tree they were
// built from, on the same machine the model is running on. A missing source
// is not an error: a project that has never built them has nothing to seed.
func seedDerivedState(root, dir string) error {
	dst := app.StateDir(dir)
	if err := os.MkdirAll(dst, logDirMode); err != nil {
		return fmt.Errorf("replay: creating %s: %w", dst, err)
	}

	for _, name := range app.DerivedState() {
		//nolint:gosec // a fixed name under the project
		body, err := os.ReadFile(filepath.Join(app.StateDir(root), name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("replay: reading %s: %w", name, err)
		}

		//nolint:gosec // name is one of a fixed list, not input
		if err := os.WriteFile(filepath.Join(dst, name), body, logFileMode); err != nil {
			return fmt.Errorf("replay: seeding %s: %w", name, err)
		}
	}

	return nil
}

// scratchBase is where a replay workspace goes. The sandbox redirects a
// run's TMPDIR inside the workspace, so the workspace path is the prefix of
// every unix socket the run's own tests create, and macOS's per-user temp
// dir is 49 characters before anything is added: a replay under it failed
// this repo's daemon tests on the 104-byte sun_path limit, and the run read
// that as a pre-existing failure. /tmp is four.
func scratchBase() string {
	const short = "/tmp"

	if fi, err := os.Stat(short); err == nil && fi.IsDir() {
		return short
	}

	return os.TempDir()
}

// stopReason is the loop's own stop where it reached one. A run the loop
// never finished has none, and recording it as complete would count a crash
// as a finished task.
func stopReason(stop string, runErr error) string {
	if stop != "" {
		return stop
	}

	if runErr != nil {
		return "error"
	}

	return "unknown"
}

// replayReport prints every recorded run of one task and diffs the last two.
func replayReport(root, task string) error {
	recs, err := replay.Load(filepath.Join(root, replay.DefaultRecordsPath))
	if err != nil {
		return err //nolint:wrapcheck // Load already names the file and the failure
	}

	return replay.Report(recs, task, os.Stdout) //nolint:wrapcheck // Report's error already names the writer
}

// replayLabel names the lane a record measures. The commit the run was
// built from is the answer whenever the caller does not give a better one,
// since that is what a later reader needs to find the change.
func replayLabel(ctx context.Context, root string, opt options) string {
	if opt.replayLabel != "" {
		return opt.replayLabel
	}

	//nolint:gosec // the arguments are fixed; only the project root varies, and it is not model input
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unlabeled"
	}

	return strings.TrimSpace(string(out))
}
