package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/transcript"
)

// Defaults for the bench flags.
const (
	defaultTasks   = "bench/tasks.toml"
	defaultRecords = "bench/records.jsonl"
	defaultTimeout = 15 * time.Minute
	harnessClaude  = "claude"
)

// usageError is a command line the bench or stats command cannot act on.
type usageError struct {
	msg string
}

func (e *usageError) Error() string { return e.msg }

const benchUsage = `usage:
  wavez bench list   [--tasks PATH]
  wavez bench run    <task-id> --arm baseline|wavez [--model M] [--n N] [--keep]
                     [--timeout D] [--tasks PATH] [--records PATH]
                     [--harness-cmd 'prog {prompt}'] [--wavez-bin PATH] [--user-settings]
  wavez bench report [task-id] [--records PATH]`

func runBench(ctx context.Context, args []string, streams stdio) error {
	if len(args) == 0 {
		return &usageError{msg: benchUsage}
	}

	switch args[0] {
	case "list":
		return benchList(args[1:], streams)
	case "run":
		return benchRun(ctx, args[1:], streams)
	case "report":
		return benchReport(args[1:], streams)
	default:
		return &usageError{msg: fmt.Sprintf("unknown bench command %q\n%s", args[0], benchUsage)}
	}
}

// parseFlags parses args that may put one positional argument before the
// flags (`run e1 --arm wavez`), which the flag package alone stops at.
func parseFlags(fs *flag.FlagSet, args []string, streams stdio) (string, error) {
	fs.SetOutput(streams.err)

	positional := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[0], args[1:]
	}

	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parsing flags: %w", err)
	}

	switch {
	case fs.NArg() == 0:
		return positional, nil
	case positional == "" && fs.NArg() == 1:
		return fs.Arg(0), nil
	default:
		return "", &usageError{msg: "unexpected argument " + fs.Arg(0)}
	}
}

func benchList(args []string, streams stdio) error {
	fs := flag.NewFlagSet("bench list", flag.ContinueOnError)
	tasksPath := fs.String("tasks", defaultTasks, "tasks file")

	if _, err := parseFlags(fs, args, streams); err != nil {
		return err
	}

	tasks, err := bench.LoadTasks(*tasksPath)
	if err != nil {
		return fmt.Errorf("loading tasks: %w", err)
	}

	for i := range tasks {
		t := &tasks[i]
		if _, err := fmt.Fprintf(streams.out, "%-5s %2d check(s)  %s\n", t.ID, len(t.Checks), t.Prompt); err != nil {
			return fmt.Errorf("writing task list: %w", err)
		}
	}

	return nil
}

// runOptions is the parsed command line of `bench run`.
type runOptions struct {
	task         string
	arm          string
	model        string
	tasks        string
	records      string
	harnessCmd   string
	wavezBin     string
	n            int
	timeout      time.Duration
	keep         bool
	userSettings bool
}

func parseRun(args []string, streams stdio) (runOptions, error) {
	var o runOptions

	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	fs.StringVar(&o.arm, "arm", "", "baseline or wavez (required)")
	fs.StringVar(&o.model, "model", "", "model to ask the harness for")
	fs.IntVar(&o.n, "n", 1, "how many times to run the task")
	fs.BoolVar(&o.keep, "keep", false, "leave each workspace behind")
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "bound on one harness run")
	fs.StringVar(&o.tasks, "tasks", defaultTasks, "tasks file")
	fs.StringVar(&o.records, "records", defaultRecords, "records file to append to")
	fs.StringVar(&o.harnessCmd, "harness-cmd", "", "run this argv template instead of Claude Code")
	fs.StringVar(&o.wavezBin, "wavez-bin", "", "wavez binary the wavez arm wires in (default: this one)")
	fs.BoolVar(&o.userSettings, "user-settings", false, "let Claude Code load user-level settings")

	task, err := parseFlags(fs, args, streams)
	if err != nil {
		return o, err
	}

	o.task = task

	switch {
	case o.task == "":
		return o, &usageError{msg: "bench run needs a task id\n" + benchUsage}
	case o.arm == "":
		return o, &usageError{msg: "bench run needs --arm baseline or --arm wavez"}
	case o.n < 1:
		return o, &usageError{msg: "--n must be at least 1"}
	}

	return o, nil
}

func benchRun(ctx context.Context, args []string, streams stdio) error {
	opts, err := parseRun(args, streams)
	if err != nil {
		return err
	}

	arm, err := bench.ParseArm(opts.arm)
	if err != nil {
		return fmt.Errorf("reading --arm: %w", err)
	}

	tasks, err := bench.LoadTasks(opts.tasks)
	if err != nil {
		return fmt.Errorf("loading tasks: %w", err)
	}

	task, err := bench.FindTask(tasks, opts.task)
	if err != nil {
		return fmt.Errorf("choosing a task: %w", err)
	}

	runner, err := newRunner(ctx, &opts)
	if err != nil {
		return err
	}

	for i := 1; i <= opts.n; i++ {
		rec, err := runner.Run(ctx, &task, arm, opts.model)
		if err != nil {
			return fmt.Errorf("run %d of %d: %w", i, opts.n, err)
		}

		if err := bench.AppendRecord(opts.records, &rec); err != nil {
			return fmt.Errorf("recording run %d: %w", i, err)
		}

		if _, err := fmt.Fprintln(streams.out, runLine(&rec, i, opts.n)); err != nil {
			return fmt.Errorf("writing run line: %w", err)
		}
	}

	return nil
}

// newRunner builds the runner from the process's surroundings: its own
// binary, its environment, and where Claude Code keeps transcripts.
func newRunner(ctx context.Context, opts *runOptions) (*bench.Runner, error) {
	runner := &bench.Runner{
		HarnessName:    harnessClaude,
		Harness:        &bench.Claude{UserSettings: opts.userSettings},
		Env:            os.Environ(),
		Timeout:        opts.timeout,
		Keep:           opts.keep,
		TranscriptRoot: transcriptRoot(),
		WavezVersion:   version,
	}

	if opts.harnessCmd != "" {
		harness, err := bench.NewCommand(opts.harnessCmd)
		if err != nil {
			return nil, fmt.Errorf("reading --harness-cmd: %w", err)
		}

		runner.Harness = harness
		runner.HarnessName = strings.Fields(opts.harnessCmd)[0]
		runner.TranscriptRoot = ""
	}

	bin := opts.wavezBin
	if bin == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("finding this binary: %w", err)
		}

		bin = self
	} else {
		runner.WavezVersion = bench.BinaryVersion(ctx, bin)
	}

	abs, err := filepath.Abs(bin)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", bin, err)
	}

	runner.WavezBin = abs

	return runner, nil
}

// transcriptRoot is where Claude Code keeps one folder of transcripts per
// project, or empty when no home directory can be found.
func transcriptRoot() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".claude", "projects")
}

// runLine is the one line printed as a run finishes.
func runLine(rec *bench.Record, i, n int) string {
	held := 0

	for _, c := range rec.Checks {
		if c.Pass {
			held++
		}
	}

	verdict := "FAIL"
	if rec.Pass {
		verdict = "pass"
	}

	sweeps := "-"
	if rec.Transcript != nil {
		sweeps = strconv.Itoa(rec.Transcript.Sweeps)
	}

	line := fmt.Sprintf("%s %s %d/%d %s stop=%s checks=%d/%d turns=%d cost=$%.4f wall=%s sweeps=%s served=%s",
		rec.TaskID, rec.Arm, i, n, verdict, rec.Stop, held, len(rec.Checks), rec.Turns, rec.CostUSD,
		(time.Duration(rec.WallMS) * time.Millisecond).Round(time.Second), sweeps, orDash(rec.Served))

	if rec.Workspace != "" {
		line += " workspace=" + rec.Workspace
	}

	if rec.Error != "" {
		line += " error=" + rec.Error
	}

	return line
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

func benchReport(args []string, streams stdio) error {
	fs := flag.NewFlagSet("bench report", flag.ContinueOnError)
	recordsPath := fs.String("records", defaultRecords, "records file")

	task, err := parseFlags(fs, args, streams)
	if err != nil {
		return err
	}

	recs, err := bench.LoadRecords(*recordsPath)
	if err != nil {
		return fmt.Errorf("loading records: %w", err)
	}

	if err := bench.Report(streams.out, recs, task); err != nil {
		return fmt.Errorf("reporting: %w", err)
	}

	return nil
}

func runStats(_ context.Context, args []string, streams stdio) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return &usageError{msg: "usage: wavez stats <session-id | path/to/transcript.jsonl>"}
	}

	path, err := transcriptPath(args[0])
	if err != nil {
		return err
	}

	stats, err := transcript.ReadSession(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	return printStats(streams.out, path, &stats)
}

// transcriptPath takes a transcript file as it is and looks anything else
// up as a session id.
func transcriptPath(arg string) (string, error) {
	if _, err := os.Stat(arg); err == nil { //nolint:gosec // the owner names the transcript to read
		return arg, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("checking %s: %w", arg, err)
	}

	path, err := transcript.Find(transcriptRoot(), arg)
	if err != nil {
		return "", fmt.Errorf("finding the session: %w", err)
	}

	return path, nil
}

func printStats(w io.Writer, path string, stats *transcript.Stats) error {
	if _, err := fmt.Fprintf(w, "session       %s\n", path); err != nil {
		return fmt.Errorf("writing session stats: %w", err)
	}

	if err := stats.Render(w); err != nil {
		return fmt.Errorf("rendering %s: %w", path, err)
	}

	return nil
}
