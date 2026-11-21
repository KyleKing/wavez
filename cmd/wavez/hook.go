package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/refactor"
)

// hookLogEnv names a file `wavez hook` appends one JSON line per event to.
const hookLogEnv = hook.TraceEnv

const (
	errInitUsage = exitError(2)
	// A failed hook exits 1 and never 2: Claude Code reads 2 from a hook as
	// "block", and any other non-zero status as an error that holds nothing.
	errHookFailed = exitError(1)
	logFileMode   = 0o600
)

// runInit wires the hooks, the refactor skill, and the state directory's
// ignore file into a project.
func runInit(_ context.Context, args []string, streams stdio) error {
	flags := flag.NewFlagSet("wavez init", flag.ContinueOnError)
	flags.SetOutput(streams.err)

	dir := flags.String("dir", ".", "project directory to set up")
	bin := flags.String("bin", "", "wavez binary the hooks call (default: wavez on PATH)")
	show := flags.Bool("print", false, "show what would be written and write nothing")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errInitUsage
	}

	install := hook.Install{Dir: *dir, Bin: *bin, Skill: refactor.Skill}

	changes, err := install.Plan()
	if err != nil {
		return fmt.Errorf("planning: %w", err)
	}

	var report strings.Builder

	for _, change := range changes {
		if *show {
			fmt.Fprintf(&report, "# %s (%s)\n%s\n", change.Path, change.Action(), change.Content)

			continue
		}

		fmt.Fprintf(&report, "%s %s\n", change.Action(), change.Path)
	}

	if !*show {
		if err := install.Apply(changes); err != nil {
			return fmt.Errorf("installing: %w", err)
		}
	}

	if _, err := io.WriteString(streams.out, report.String()); err != nil {
		return fmt.Errorf("reporting: %w", err)
	}

	return nil
}

// runHook handles the hook event on stdin. A failure of wavez itself is one
// line on stderr and a non-blocking status, so it never holds the session.
func runHook(ctx context.Context, _ []string, streams stdio) error {
	var (
		opts  []hook.Option
		trace bytes.Buffer
	)

	// The harness starts a hook in the project directory. A config that
	// does not load keeps the default cap and leaves the route hint off
	// here, and is reported by the gate run itself, which loads it again.
	if dir, err := os.Getwd(); err == nil {
		root := hook.FindRoot(dir)

		if cfg, err := config.Load(root); err == nil {
			opts = append(opts, hook.WithMaxRounds(cfg.MaxRounds))

			if cfg.Route {
				opts = append(opts, hook.WithRoute(decide.FromConfig(root, cfg.Decide)))
			}
		}
	}

	logPath := os.Getenv(hookLogEnv)
	if logPath != "" {
		opts = append(opts, hook.WithTrace(&trace))
	}

	err := hook.New(hook.Gate{Runner: gate.New()}, opts...).Handle(ctx, streams.in, streams.out)
	if logPath != "" {
		err = errors.Join(err, appendTo(logPath, trace.Bytes()))
	}

	if err == nil {
		return nil
	}

	line := "wavez hook: " + strings.Join(strings.Fields(err.Error()), " ") + "\n"
	if _, writeErr := io.WriteString(streams.err, line); writeErr != nil {
		return errors.Join(errHookFailed, writeErr)
	}

	return errHookFailed
}

func appendTo(path string, content []byte) error {
	const flags = os.O_APPEND | os.O_CREATE | os.O_WRONLY

	file, err := os.OpenFile(path, flags, logFileMode) //nolint:gosec // the path is the user's own choice
	if err != nil {
		return fmt.Errorf("opening %s: %w", hookLogEnv, err)
	}

	_, err = file.Write(content)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("writing %s: %w", hookLogEnv, err)
	}

	return nil
}
