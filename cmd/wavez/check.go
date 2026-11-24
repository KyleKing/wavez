package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/session"
)

// errUsage is a check invocation the flags cannot make sense of.
var errUsage = errors.New("usage: wavez check [--stage edit|stop] [--json] [--root DIR] [files...]")

// The statuses wavez check exits with beyond 0 for a pass: a blocking check
// failed, or none failed and at least one could not run, which has not
// shown the change is sound.
const (
	errChecksFailed    = exitError(1)
	errChecksUnchecked = exitError(3)
)

// handBudget bounds a run started from the shell when neither
// WAVEZ_STOP_BUDGET nor stop_budget says otherwise. Claude Code's shell tool
// gives up on a command after two minutes, so a run has to end before
// then, with what it found, rather than be killed with nothing.
const handBudget = 100 * time.Second

// runCheck runs the checks the changed files select: the fixers, then the
// checks side by side, each pass reused while its inputs are unchanged. It
// prints one line on a pass, the failing lines on a failure, and on stderr
// what could not run.
func runCheck(ctx context.Context, args []string, streams stdio) error {
	return checkAs(ctx, "check", args, streams)
}

// runGate is the older name of check, kept for scripts that call it.
func runGate(ctx context.Context, args []string, streams stdio) error {
	return checkAs(ctx, "gate", args, streams)
}

func checkAs(ctx context.Context, name string, args []string, streams stdio) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(streams.err)

	stage := flags.String("stage", string(gate.StageStop), "which checks to run: edit or stop")
	asJSON := flags.Bool("json", false, "print the report as JSON")
	rootFlag := flags.String("root", "", "project root (default: the project the working directory is in)")

	if err := flags.Parse(args); err != nil {
		return exitError(usageStatus)
	}

	if s := gate.Stage(*stage); s != gate.StageEdit && s != gate.StageStop {
		return fmt.Errorf("%w: --stage is %q", errUsage, *stage)
	}

	root := *rootFlag
	if root == "" {
		root = hook.FindRoot(".")
	}

	ctx, changed, source, err := checkScope(ctx, root, flags.Args())
	if errors.Is(err, gate.ErrNotRepository) {
		return say(streams, "wavez "+name+": not a repository and no files named, nothing checked\n")
	}

	if err != nil {
		return err
	}

	for _, file := range flags.Args() {
		if _, statErr := os.Lstat(file); statErr != nil {
			sayf(streams.err, "wavez %s: %s does not exist; it is checked as a file that was deleted\n", name, file)
		}
	}

	report, err := gate.New(handOptions(projectConfig(root))...).Run(ctx, root, changed, gate.Stage(*stage))
	if err != nil {
		return fmt.Errorf("running checks: %w", err)
	}

	if err := printReport(streams, name, report, *asJSON); err != nil {
		return err
	}

	switch {
	case len(changed) == 0:
		sayf(streams.err, "wavez %s: nothing to check: no file changed in %s\n", name, source)
	case len(report.Results) == 0:
		sayf(streams.err, "wavez %s: nothing to check: no %s-stage check is selected by the %d changed file(s)\n",
			name, *stage, len(changed))
	}

	return exitFor(report)
}

func exitFor(report gate.Report) error {
	switch {
	case report.Failed():
		return errChecksFailed
	case report.Abstained():
		return errChecksUnchecked
	default:
		return nil
	}
}

// checkScope returns the changed set relative to root and what said so. A
// named file wins. Without one, a run inside an agent session that wavez
// follows takes what the session changed and compares a failure with the
// tree it started from, which is what the stop gate would do. Anywhere else
// the version control's own view of the tree is used.
func checkScope(
	ctx context.Context, root string, named []string,
) (context.Context, []string, string, error) {
	if len(named) > 0 {
		changed, err := namedFiles(root, named)

		return ctx, changed, "the files named", err
	}

	if sessionCtx, changed, ok, err := sessionScope(ctx, root); ok || err != nil {
		return sessionCtx, changed, "the session", err
	}

	changed, err := gate.Changed(ctx, root)
	if err != nil {
		return ctx, nil, "", fmt.Errorf("finding changed files: %w", err)
	}

	return ctx, changed, "the working tree", nil
}

// sessionScope returns what the session named by SessionEnv changed, with
// its baseline on the context, and false when no such session is stored
// under root.
func sessionScope(ctx context.Context, root string) (context.Context, []string, bool, error) {
	id := os.Getenv(hook.SessionEnv)
	if id == "" {
		return ctx, nil, false, nil
	}

	resolved, err := filepath.Abs(root)
	if err != nil {
		return ctx, nil, false, nil //nolint:nilerr // a root that does not resolve has no session to read
	}

	store := session.NewStore(resolved)

	state, ok := store.Read(id)
	if !ok {
		return ctx, nil, false, nil
	}

	changed, err := store.Changed(ctx, state)
	if err != nil {
		return ctx, nil, true, fmt.Errorf("finding what the session changed: %w", err)
	}

	return hook.WithSessionBaseline(ctx, state.Baseline), changed, true, nil
}

// handOptions is the runner's options for a run started from the shell.
func handOptions(cfg config.Config) []gate.Option {
	if stopBudget(cfg) > 0 {
		return gateOptions(cfg)
	}

	return []gate.Option{gate.WithBudget(handBudget)}
}

// projectConfig is root's configuration for the settings a gate run reads
// before it runs. One that does not load gives the defaults, and the run
// reports the error where it reads the checks.
func projectConfig(root string) config.Config {
	cfg, err := config.Load(root)
	if err != nil {
		return config.Config{}
	}

	return cfg
}

// namedFiles returns the named files relative to root, read relative to
// the working directory the way a shell hands them over.
func namedFiles(root string, named []string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %s: %w", root, err)
	}

	out := make([]string, 0, len(named))

	for _, name := range named {
		abs, err := filepath.Abs(name)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", name, err)
		}

		rel, err := filepath.Rel(absRoot, abs)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("%w: %s is outside the root %s", errUsage, name, root)
		}

		out = append(out, filepath.ToSlash(rel))
	}

	return out, nil
}

func say(streams stdio, text string) error {
	if _, err := fmt.Fprint(streams.out, text); err != nil {
		return fmt.Errorf("printing report: %w", err)
	}

	return nil
}

// printReport prints the failing lines, or one line naming what passed, on
// stdout, and on stderr the lines for the person: what could not run, and
// what failed before the session began.
func printReport(streams stdio, name string, report gate.Report, asJSON bool) error {
	if asJSON {
		if report.Results == nil {
			report.Results = []gate.Result{}
		}

		enc := json.NewEncoder(streams.out)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("printing report: %w", err)
		}

		return nil
	}

	text := report.Render()
	if text == "" && len(report.Results) > 0 && !report.Abstained() {
		text = passLine(name, report)
	}

	sayf(streams.err, "%s", report.Notes())

	return say(streams, text)
}

// passLine names the checks that passed and which of them were cached.
func passLine(name string, report gate.Report) string {
	var passed, cached []string

	for i := range report.Results {
		res := &report.Results[i]
		if res.Status != gate.StatusPass || slices.Contains(passed, res.Check) {
			continue
		}

		passed = append(passed, res.Check)
		if res.Cached {
			cached = append(cached, res.Check)
		}
	}

	line := "wavez " + name + ": passed " + strings.Join(passed, ", ")
	if len(cached) > 0 {
		line += " (unchanged: " + strings.Join(cached, ", ") + ")"
	}

	return line + "\n"
}
