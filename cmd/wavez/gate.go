package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kyleking/wavez/internal/gate"
)

// errUsage is a gate invocation the flags cannot make sense of.
var errUsage = errors.New("usage: wavez gate [--stage edit|stop] [--json] [--root DIR] [files...]")

// runGate runs the checks the changed files select and prints what an agent
// needs to know, which is nothing when they pass. With no files it asks git
// what changed. The status is 1 when a blocking check failed, after the
// report is printed.
func runGate(ctx context.Context, args []string, streams stdio) error {
	flags := flag.NewFlagSet("gate", flag.ContinueOnError)
	flags.SetOutput(streams.err)

	stage := flags.String("stage", string(gate.StageStop), "which checks to run: edit or stop")
	asJSON := flags.Bool("json", false, "print the report as JSON")
	root := flags.String("root", ".", "project root")

	if err := flags.Parse(args); err != nil {
		return exitError(2) //nolint:mnd // the conventional status for a usage error, which flag already printed
	}

	if s := gate.Stage(*stage); s != gate.StageEdit && s != gate.StageStop {
		return fmt.Errorf("%w: --stage is %q", errUsage, *stage)
	}

	changed, err := gateFiles(ctx, *root, flags.Args())
	if errors.Is(err, gate.ErrNotRepository) {
		return say(streams, "wavez gate: not a git repository and no files named, nothing checked\n")
	}

	if err != nil {
		return err
	}

	for _, name := range flags.Args() {
		if _, statErr := os.Lstat(name); statErr != nil {
			sayf(streams.err, "wavez gate: %s does not exist; it is checked as a file that was deleted\n", name)
		}
	}

	report, err := gate.New(gateOptions()...).Run(ctx, *root, changed, gate.Stage(*stage))
	if err != nil {
		return fmt.Errorf("running checks: %w", err)
	}

	if err := printReport(streams, report, *asJSON); err != nil {
		return err
	}

	// A person who ran this by hand and saw nothing cannot tell a pass
	// from a run that examined nothing. The hook never comes this way.
	switch {
	case len(changed) == 0:
		sayf(streams.err, "wavez gate: nothing to check: git reports no changed file\n")
	case len(report.Results) == 0:
		sayf(streams.err, "wavez gate: nothing to check: no %s-stage check is selected by the %d changed file(s)\n",
			*stage, len(changed))
	}

	if report.Failed() {
		return exitError(1)
	}

	return nil
}

// gateFiles returns the changed set relative to root: the named files, which
// are read relative to the working directory the way a shell hands them
// over, or git's view of the tree when none is named.
func gateFiles(ctx context.Context, root string, named []string) ([]string, error) {
	if len(named) == 0 {
		changed, err := gate.Changed(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("finding changed files: %w", err)
		}

		return changed, nil
	}

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

func printReport(streams stdio, report gate.Report, asJSON bool) error {
	if !asJSON {
		return say(streams, report.Render()+report.Notes())
	}

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
