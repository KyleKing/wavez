package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/warm"
)

// errWarmUsage is a warm invocation without the session it is for.
var errWarmUsage = errors.New("usage: wavez warm --root DIR --session ID")

// runWarm is what the hooks start in the background: the session's checks,
// run for what they leave behind and not for what they print. It is the
// session's one warm run while it lives, and ends when a newer one asks it
// to.
func runWarm(ctx context.Context, args []string, streams stdio) error {
	flags := flag.NewFlagSet("warm", flag.ContinueOnError)
	flags.SetOutput(streams.err)

	root := flags.String("root", "", "project root")
	id := flags.String("session", "", "session the run is for")

	if err := flags.Parse(args); err != nil {
		return exitError(usageStatus)
	}

	if *root == "" || *id == "" {
		return errWarmUsage
	}

	release, err := warm.Hold(ctx, *root, *id)
	if err != nil {
		return fmt.Errorf("warm: %w", err)
	}
	defer release()

	if err := os.Setenv(hook.SessionEnv, *id); err != nil {
		return fmt.Errorf("warm: naming the session: %w", err)
	}

	quiet := stdio{in: streams.in, out: io.Discard, err: io.Discard}

	// A failing check is an answer here, not this command's failure: the
	// run that reports it is the one somebody asked for.
	if err := checkWith(ctx, "check", []string{"--root", *root}, quiet, runAhead); err != nil {
		var status exitError
		if !errors.As(err, &status) {
			return err
		}
	}

	return nil
}

// warmStart is how a hook starts a warm run: this binary again, on its own.
func warmStart(root, id string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this binary: %w", err)
	}

	return warm.Start(self, root, id) //nolint:wrapcheck // the warm package names the step
}
