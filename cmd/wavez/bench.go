package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	// Long enough for a cold `go test` of a pinned tree, and short enough
	// that a check which hangs does not hold up the whole schedule.
	defaultCheckTimeout = 10 * time.Minute
	harnessClaude       = "claude"
	// The narrowest column `bench list` gives task ids.
	minIDWidth = 5
)

// usageError is a command line the bench or stats command cannot act on.
type usageError struct {
	msg string
}

func (e *usageError) Error() string { return e.msg }

const benchUsage = `usage:
  wavez bench list   [--tasks PATH]
  wavez bench run    <task-id[,task-id...]> | --all  --arm ARM[,ARM...]
                     [--model M] [--n N] [--seed S] [--keep]
                     [--timeout D] [--check-timeout D] [--tasks PATH] [--records PATH]
                     [--harness-cmd 'prog {prompt}'] [--wavez-bin PATH] [--overlay DIR] [--user-settings]
  wavez bench report [task-id] [--against ARM] [--records PATH]

arms: baseline, wavez, wavez-quiet, wavez-route. Several arms are interleaved:
each of the N repetitions runs every task once under every arm, the arms in a
freshly shuffled order for each task.`

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

	width := minIDWidth
	for i := range tasks {
		width = max(width, len(tasks[i].ID))
	}

	for i := range tasks {
		t := &tasks[i]
		_, err := fmt.Fprintf(streams.out, "%-*s %2d check(s)  %s\n", width, t.ID, len(t.Checks), t.Prompt)
		if err != nil {
			return fmt.Errorf("writing task list: %w", err)
		}
	}

	return nil
}

// runOptions is the parsed command line of `bench run`.
type runOptions struct {
	task         string
	model        string
	tasks        string
	records      string
	harnessCmd   string
	wavezBin     string
	overlay      string
	arms         armList
	seed         uint64
	n            int
	timeout      time.Duration
	checkTimeout time.Duration
	keep         bool
	userSettings bool
	all          bool
}

// armList is the --arm flag: arm names separated by commas, given once or
// several times.
type armList []bench.Arm

func (l *armList) String() string {
	names := make([]string, 0, len(*l))
	for _, arm := range *l {
		names = append(names, string(arm))
	}

	return strings.Join(names, ",")
}

func (l *armList) Set(value string) error {
	for name := range strings.SplitSeq(value, ",") {
		arm, err := bench.ParseArm(strings.TrimSpace(name))
		if err != nil {
			return fmt.Errorf("reading --arm: %w", err)
		}

		if slices.Contains(*l, arm) {
			return &usageError{msg: fmt.Sprintf("arm %s is named twice", arm)}
		}

		*l = append(*l, arm)
	}

	return nil
}

func parseRun(args []string, streams stdio) (runOptions, error) {
	var o runOptions

	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	fs.Var(&o.arms, "arm", "arms to run, separated by commas: "+armNames()+" (required)")
	fs.StringVar(&o.model, "model", "", "model to ask the harness for")
	fs.IntVar(&o.n, "n", 1, "how many times to run the task under each arm")
	fs.Uint64Var(&o.seed, "seed", 0, "seed for the order of the arms within a repetition (default: a new one)")
	fs.BoolVar(&o.keep, "keep", false, "leave each workspace behind")
	fs.BoolVar(&o.all, "all", false, "run every task in the tasks file")
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "bound on one harness run")
	fs.DurationVar(&o.checkTimeout, "check-timeout", defaultCheckTimeout, "bound on one check that runs a program")
	fs.StringVar(&o.tasks, "tasks", defaultTasks, "tasks file")
	fs.StringVar(&o.records, "records", defaultRecords, "records file to append to")
	fs.StringVar(&o.harnessCmd, "harness-cmd", "", "run this argv template instead of Claude Code")
	fs.StringVar(&o.wavezBin, "wavez-bin", "", "wavez binary the wired arms use (default: this one)")
	fs.StringVar(&o.overlay, "overlay", "", "directory of files added to each wired arm's workspace")
	fs.BoolVar(&o.userSettings, "user-settings", false, "let Claude Code load user-level settings")

	task, err := parseFlags(fs, args, streams)
	if err != nil {
		return o, err
	}

	o.task = task

	switch {
	case o.task == "" && !o.all:
		return o, &usageError{msg: "bench run needs a task id or --all\n" + benchUsage}
	case o.task != "" && o.all:
		return o, &usageError{msg: "bench run takes task ids or --all, not both"}
	case len(o.arms) == 0:
		return o, &usageError{msg: "bench run needs --arm with one or more of " + armNames()}
	case o.n < 1:
		return o, &usageError{msg: "--n must be at least 1"}
	}

	return o, nil
}

func armNames() string {
	var all armList = bench.Arms()

	return all.String()
}

// flags renders the settings that change what a run does, so that a report
// never pools runs made under different ones.
func (o *runOptions) flags() string {
	parts := []string{"timeout=" + o.timeout.String(), "check-timeout=" + o.checkTimeout.String()}

	if o.harnessCmd != "" {
		return strings.Join(append(parts, "harness-cmd="+strconv.Quote(o.harnessCmd)), " ")
	}

	if o.userSettings {
		parts = append(parts, "user-settings")
	}

	return strings.Join(parts, " ")
}

func benchRun(ctx context.Context, args []string, streams stdio) error {
	opts, err := parseRun(args, streams)
	if err != nil {
		return err
	}

	tasks, err := chooseTasks(&opts)
	if err != nil {
		return err
	}

	runner, err := newRunner(ctx, &opts)
	if err != nil {
		return err
	}

	interleaved := len(opts.arms) > 1
	if interleaved {
		if err := announceSeed(&opts, streams); err != nil {
			return err
		}
	}

	// One block is one task in one repetition: every arm once.
	for block, order := range bench.Schedule(opts.arms, opts.n*len(tasks), opts.seed) {
		task, rep := &tasks[block%len(tasks)], block/len(tasks)+1

		for slot, arm := range order {
			rec, err := runner.Run(ctx, task, arm, opts.model)
			if err != nil {
				return fmt.Errorf("%s under %s, run %d of %d: %w", task.ID, arm, rep, opts.n, err)
			}

			if interleaved {
				rec.Seed, rec.Rep, rec.Slot = opts.seed, rep, slot+1
			}

			if err := keep(&opts, &rec, rep, streams); err != nil {
				return err
			}
		}
	}

	return nil
}

// keep appends a finished run to the records file and prints its line.
func keep(opts *runOptions, rec *bench.Record, rep int, streams stdio) error {
	if err := bench.AppendRecord(opts.records, rec); err != nil {
		return fmt.Errorf("recording %s under %s, run %d: %w", rec.TaskID, rec.Arm, rep, err)
	}

	if _, err := fmt.Fprintln(streams.out, rec.Summary(rep, opts.n)); err != nil {
		return fmt.Errorf("writing run line: %w", err)
	}

	return nil
}

// chooseTasks loads the tasks file and picks the tasks named, in the order
// named, or all of them.
func chooseTasks(opts *runOptions) ([]bench.Task, error) {
	tasks, err := bench.LoadTasks(opts.tasks)
	if err != nil {
		return nil, fmt.Errorf("loading tasks: %w", err)
	}

	if opts.all {
		return tasks, nil
	}

	var chosen []bench.Task

	for id := range strings.SplitSeq(opts.task, ",") {
		task, err := bench.FindTask(tasks, strings.TrimSpace(id))
		if err != nil {
			return nil, fmt.Errorf("choosing a task: %w", err)
		}

		if slices.ContainsFunc(chosen, func(t bench.Task) bool { return t.ID == task.ID }) {
			return nil, &usageError{msg: fmt.Sprintf("task %s is named twice", task.ID)}
		}

		chosen = append(chosen, task)
	}

	return chosen, nil
}

// announceSeed settles the seed of an interleaved run and prints it, so the
// order the arms ran in can be had again.
func announceSeed(opts *runOptions, streams stdio) error {
	if opts.seed == 0 {
		seed, err := bench.NewSeed()
		if err != nil {
			return fmt.Errorf("interleaving the arms: %w", err)
		}

		opts.seed = seed
	}

	_, err := fmt.Fprintf(streams.out, "seed %d: each repetition runs %s in a freshly shuffled order\n",
		opts.seed, opts.arms.String())
	if err != nil {
		return fmt.Errorf("writing the seed: %w", err)
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
		CheckTimeout:   opts.checkTimeout,
		Flags:          opts.flags(),
		Keep:           opts.keep,
		TranscriptRoot: transcriptRoot(),
		CacheDir:       repoCache(),
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

	// A wired arm tells the model to run `wavez`, and finds it by this
	// binary's directory being first on PATH. Under any other name the
	// command is not found and the arm is measured without it.
	wired := slices.ContainsFunc(opts.arms, func(arm bench.Arm) bool { return arm != bench.ArmBaseline })
	if name := strings.TrimSuffix(filepath.Base(abs), ".exe"); wired && name != "wavez" {
		return nil, fmt.Errorf("%w: the binary is %s", errBinaryName, abs)
	}

	runner.WavezBin = abs

	if runner.Build, err = bench.BinaryDigest(abs); err != nil {
		return nil, fmt.Errorf("identifying the build: %w", err)
	}

	return runner, overlaid(runner, opts.overlay)
}

// overlaid gives the runner the --overlay directory and names its content in
// the flags every record carries.
func overlaid(runner *bench.Runner, dir string) error {
	if dir == "" {
		return nil
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", dir, err)
	}

	digest, err := bench.TreeDigest(abs)
	if err != nil {
		return fmt.Errorf("reading --overlay: %w", err)
	}

	runner.Overlay, runner.Flags = abs, runner.Flags+" overlay="+digest

	return nil
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

// repoCache is where the mirrors of repositories that tasks name by URL are
// kept between invocations, or empty when there is no user cache directory
// and each run fetches its own.
func repoCache() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "wavez", "bench-repos")
}

func benchReport(args []string, streams stdio) error {
	fs := flag.NewFlagSet("bench report", flag.ContinueOnError)
	recordsPath := fs.String("records", defaultRecords, "records file")
	against := fs.String("against", string(bench.ArmBaseline), "arm every other arm is compared with")

	task, err := parseFlags(fs, args, streams)
	if err != nil {
		return err
	}

	base, err := bench.ParseArm(*against)
	if err != nil {
		return fmt.Errorf("reading --against: %w", err)
	}

	recs, err := bench.LoadRecords(*recordsPath)
	if err != nil {
		return fmt.Errorf("loading records: %w", err)
	}

	if err := bench.Report(streams.out, recs, bench.ReportOptions{Task: task, Against: base}); err != nil {
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
