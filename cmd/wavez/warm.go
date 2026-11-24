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
	"time"

	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/warm"
)

// prewarmBudget bounds filling the build cache when a session starts.
const prewarmBudget = 3 * time.Minute

// errWarmUsage is a warm invocation without the session it is for.
var errWarmUsage = errors.New("usage: wavez warm --root DIR --session ID [--prewarm]")

// runWarm is what the hooks start in the background: the session's checks,
// run for what they leave behind and not for what they print. With
// --prewarm it compiles the module instead, which is what a session's first
// build or test would otherwise wait for in a tree that is new to the build
// cache. It is the session's one warm run while it lives, and ends when a
// newer one asks it to.
func runWarm(ctx context.Context, args []string, streams stdio) error {
	flags := flag.NewFlagSet("warm", flag.ContinueOnError)
	flags.SetOutput(streams.err)

	root := flags.String("root", "", "project root")
	id := flags.String("session", "", "session the run is for")
	prewarm := flags.Bool("prewarm", false, "compile the module and check nothing")

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

	if *prewarm {
		compileModule(ctx, *root)

		return nil
	}

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

// compileModule compiles a Go module's packages and their tests, linking
// nothing. A root that is not a Go module, a missing go, and a build that
// fails are all the same to it: there was nothing to keep warm.
func compileModule(ctx context.Context, root string) {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, prewarmBudget)
	defer cancel()

	//nolint:gosec // go itself
	cmd := exec.CommandContext(ctx, goBin, "list", "-export", "-trimpath", "-test", "-f", `{{""}}`, "./...")
	cmd.Dir = root

	_ = cmd.Run() //nolint:errcheck // see above
}

// warmStart is how a hook starts a warm run: this binary again, on its own.
func warmStart(root, session string, prewarm bool) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this binary: %w", err)
	}

	if prewarm {
		return warm.Start(self, root, session, "--prewarm") //nolint:wrapcheck // the warm package names the step
	}

	return warm.Start(self, root, session) //nolint:wrapcheck // the warm package names the step
}
