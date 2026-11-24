package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/refactor"
)

const errInitUsage = exitError(2)

// errInit is an init that was asked for something it will not do.
var errInit = errors.New("init")

// initFlags is one `wavez init` command line.
type initFlags struct {
	dir    string
	bin    string
	show   bool
	remove bool
}

func parseInit(args []string, stderr io.Writer) (initFlags, error) {
	var parsed initFlags

	flags := flag.NewFlagSet("wavez init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	flags.StringVar(&parsed.dir, "dir", "",
		"project directory to set up (default: the git repository the working directory is in)")
	flags.StringVar(&parsed.bin, "bin", "", "wavez binary the hooks call (default: wavez on PATH)")
	flags.BoolVar(&parsed.show, "print", false, "show what would be written and write nothing")
	flags.BoolVar(&parsed.remove, "remove", false, "take out the hooks and the skill init installed, and nothing else")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return parsed, err //nolint:wrapcheck // the caller reads it as a request for help
		}

		return parsed, errInitUsage
	}

	if flags.NArg() > 0 {
		sayf(stderr, "wavez init: unexpected argument %q; the project directory is given with --dir\n", flags.Arg(0))

		return parsed, errInitUsage
	}

	return parsed, nil
}

// runInit wires the hooks, the refactor skill, and the state directory's
// ignore file into a project, or with --remove takes the first two out.
func runInit(_ context.Context, args []string, streams stdio) error {
	parsed, err := parseInit(args, streams.err)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	if err != nil {
		return err
	}

	dir, err := initDir(parsed.dir)
	if err != nil {
		return err
	}

	install := hook.Install{
		Dir: dir, Bin: parsed.bin, Skill: refactor.Skill, StopBudget: projectConfig(dir).StopBudget,
	}

	var (
		changes []hook.Change
		notes   []string
	)

	if parsed.remove {
		changes, err = install.PlanRemoval()
		notes = append(notes, "left .wavez/ alone: it holds session state and caches, and is safe to delete by hand")
	} else {
		// The skill teaches `wavez rename`, `move`, and `delete`, which
		// work on Go declarations and nothing else.
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr != nil {
			install.Skill = ""

			notes = append(notes, "skipped the Go refactor skill: "+dir+" has no go.mod")
		}

		changes, err = install.Plan()
		notes = append(notes, binaryWarning(parsed.bin)...)
	}

	if err != nil {
		return fmt.Errorf("planning: %w", err)
	}

	if !parsed.show {
		if err := install.Apply(changes); err != nil {
			return fmt.Errorf("installing: %w", err)
		}
	}

	if _, err := io.WriteString(streams.out, initReport(changes, parsed)); err != nil {
		return fmt.Errorf("reporting: %w", err)
	}

	for _, note := range notes {
		sayf(streams.err, "wavez init: %s\n", note)
	}

	return nil
}

// initDir is the project init works on: --dir when given, which must
// exist, and otherwise the repository the working directory is in, because
// Claude Code reads a project's settings from its root and a .claude made
// in a subdirectory is one it never looks at.
func initDir(named string) (string, error) {
	if named != "" {
		info, err := os.Stat(named)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("%w: --dir %s is not an existing directory", errInit, named)
		}

		return named, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("finding the working directory: %w", err)
	}

	if root, ok := hook.RepositoryRoot(cwd); ok {
		return root, nil
	}

	return cwd, nil
}

// binaryWarning says so when the installed hooks would not find wavez. The
// hooks run `wavez hook` through a shell, and one that cannot find the
// binary fails on every event of every session.
func binaryWarning(bin string) []string {
	if bin != "" {
		return nil
	}

	if _, err := exec.LookPath("wavez"); err == nil {
		return nil
	}

	fix := "--bin /path/to/wavez"
	if self, err := os.Executable(); err == nil {
		fix = "--bin " + self
	}

	return []string{
		"warning: wavez is not on PATH, so every hook would fail with `wavez: not found`. " +
			"Put it on PATH, or run `wavez init " + fix + "` to pin the binary",
	}
}

func initReport(changes []hook.Change, parsed initFlags) string {
	var report strings.Builder

	for _, change := range changes {
		if parsed.show && !change.Removed {
			fmt.Fprintf(&report, "# %s (%s)\n%s\n", change.Path, change.Action(), change.Content)

			continue
		}

		fmt.Fprintf(&report, "%s %s\n", change.Action(), change.Path)
	}

	switch {
	case len(changes) > 0:
	case parsed.remove:
		report.WriteString("nothing to remove: no wavez hook or skill is installed here\n")
	default:
		report.WriteString("nothing to change: the wavez hooks are already installed\n")
	}

	return report.String()
}
