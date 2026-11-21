// Command wavez is the deterministic layer under a coding agent: gates that
// run on what a session changed, refactors that take names instead of text,
// and a benchmark that says whether either one paid for itself.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// stdio is the process's streams, passed in so a command can be tested
// without a subprocess.
type stdio struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

// exitCode is an error that carries the status the process must exit with
// and has already printed whatever it had to say. A hook uses it, because
// the harness reads meaning from the status itself.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, streams stdio) error
}

func commands() []command {
	return []command{
		{"init", "wire wavez into this project's Claude Code settings", runInit},
		{"hook", "handle one Claude Code hook event read from stdin", runHook},
		{"gate", "run the checks selected by the changed files", runGate},
		{"rename", "rename a Go symbol and every reference to it", runRename},
		{"move", "move Go declarations to another file in the package", runMove},
		{"delete", "delete Go declarations nothing else uses", runDelete},
		{"bench", "run a pinned task under a harness and record what it cost", runBench},
		{"stats", "attribute the turns of a recorded Claude Code session", runStats},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], stdio{in: os.Stdin, out: os.Stdout, err: os.Stderr})

	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, streams stdio) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(streams.out)

		return 0
	}

	if args[0] == "version" || args[0] == "--version" {
		_, _ = fmt.Fprintf(streams.out, "wavez %s (%s, %s)\n", version, commit, date)

		return 0
	}

	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}

		return status(c.run(ctx, args[1:], streams), c.name, streams.err)
	}

	_, _ = fmt.Fprintf(streams.err, "wavez: unknown command %q\n", args[0])
	usage(streams.err)

	return 2
}

func status(err error, name string, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}

	_, _ = fmt.Fprintf(stderr, "wavez %s: %v\n", name, err)

	return 1
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: wavez <command> [arguments]")
	_, _ = fmt.Fprintln(w)

	for _, c := range commands() {
		_, _ = fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
}
