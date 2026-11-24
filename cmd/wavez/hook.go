package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
)

// stopBudgetEnv names the environment variable that sets how long one stop
// gate may run in all, as a Go duration such as "90s". It wins over the
// stop_budget key of .wavez.toml.
const stopBudgetEnv = "WAVEZ_STOP_BUDGET"

const (
	// A failed hook exits 1 and never 2: Claude Code reads 2 from a hook as
	// "block", and any other non-zero status as an error that holds nothing.
	errHookFailed = exitError(1)
	logFileMode   = 0o600
)

// errHookArgument is a hook command line holding anything but a request for
// help.
var errHookArgument = errors.New("the event is read from stdin and this argument is not understood")

// errHookPanic is a bug in wavez that surfaced while handling an event.
var errHookPanic = errors.New("internal error")

const hookUsage = `usage: wavez hook < event.json

Handles one Claude Code hook event read from stdin and prints the hook's
JSON answer, if it has one. ` + "`wavez init`" + ` wires it into a project's settings;
it is not run by hand except to try an event out.

Environment:
  WAVEZ_HOOK_LOG      file to append one JSON line per event to
  WAVEZ_STOP_BUDGET   how long a stop gate may run in all (default 2m30s); wins over
                      stop_budget in .wavez.toml
  WAVEZ_ANNOUNCE=0    do not tell the agent at session start that the gates run;
                      wins over announce in .wavez.toml
`

// runHook handles the hook event on stdin. A failure of wavez itself is one
// line on stderr and a non-blocking status, so it never holds the session.
// That includes a panic: Go exits 2 on one, which the harness reads as a
// hook asking to block.
func runHook(ctx context.Context, args []string, streams stdio) (err error) {
	if len(args) > 0 {
		// Nothing is read from stdin: a person asking for help has not
		// piped an event in, and would wait on the read forever.
		if asksForHelp(args[0]) {
			sayf(streams.out, "%s", hookUsage)

			return nil
		}

		return hookFailure(streams.err, fmt.Errorf("%w: %q", errHookArgument, args[0]))
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = hookFailure(streams.err, fmt.Errorf("%w: %v", errHookPanic, recovered))
		}
	}()

	if err := handleHook(ctx, streams); err != nil {
		return hookFailure(streams.err, err)
	}

	return nil
}

// handleHook reads the event once, finds its project once, and loads that
// project's configuration once for the handler's own settings. A config
// that does not load keeps the defaults here and is reported by the gate
// run, which reads it for the checks.
func handleHook(ctx context.Context, streams stdio) error {
	ev, err := hook.ReadEvent(streams.in)
	if err != nil {
		return err //nolint:wrapcheck // the hook package's error is the message
	}

	var (
		opts  []hook.Option
		trace bytes.Buffer
		cfg   = config.Config{Announce: true}
	)

	root, _ := hook.ResolveRoot(os.Getenv(hook.ProjectDirEnv), ev.Cwd)

	if loaded, err := config.Load(root); root != "" && err == nil {
		cfg = loaded

		opts = append(opts, hook.WithMaxRounds(cfg.MaxRounds))

		if cfg.Route {
			opts = append(opts, hook.WithRoute(decide.FromConfig(root, cfg.Decide)))
		}
	}

	if budget := stopBudget(cfg); budget > 0 {
		opts = append(opts, hook.WithStopBudget(budget))
	}

	if !cfg.StopChecks {
		opts = append(opts, hook.WithoutStopChecks())
	}

	if !announces(cfg) {
		opts = append(opts, hook.WithoutAnnouncement())
	}

	logPath := os.Getenv(hook.TraceEnv)
	if logPath != "" {
		opts = append(opts, hook.WithTrace(&trace))
	}

	handler := hook.New(hook.Gate{Runner: gate.New(gateOptions(cfg)...)}, opts...)

	err = handler.HandleEvent(ctx, ev, root, streams.out)
	if logPath != "" {
		err = errors.Join(err, appendTo(logPath, trace.Bytes()))
	}

	return err
}

// stopBudget is the stop budget in force: the environment's, else the
// file's, else zero for the gate package's default. An environment value that
// is not a positive duration is ignored, since a hook has nobody to tell.
func stopBudget(cfg config.Config) time.Duration {
	if budget, err := time.ParseDuration(os.Getenv(stopBudgetEnv)); err == nil && budget > 0 {
		return budget
	}

	return cfg.StopBudget
}

// announces reports whether a session's start tells the agent that the gates
// run. WAVEZ_ANNOUNCE=0 turns it off whatever the file says.
func announces(cfg config.Config) bool {
	return cfg.Announce && os.Getenv(hook.AnnounceEnv) != "0"
}

func gateOptions(cfg config.Config) []gate.Option {
	budget := stopBudget(cfg)
	if budget <= 0 {
		return nil
	}

	return []gate.Option{gate.WithBudget(budget)}
}

// hookFailure prints the one line a failed hook says and returns the status
// that holds nothing.
func hookFailure(stderr io.Writer, err error) error {
	line := "wavez hook: " + strings.Join(strings.Fields(err.Error()), " ") + "\n"
	if _, writeErr := io.WriteString(stderr, line); writeErr != nil {
		return errors.Join(errHookFailed, writeErr)
	}

	return errHookFailed
}

func appendTo(path string, content []byte) error {
	const flags = os.O_APPEND | os.O_CREATE | os.O_WRONLY

	file, err := os.OpenFile(path, flags, logFileMode) //nolint:gosec // the path is the user's own choice
	if err != nil {
		return fmt.Errorf("opening %s: %w", hook.TraceEnv, err)
	}

	_, err = file.Write(content)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("writing %s: %w", hook.TraceEnv, err)
	}

	return nil
}
