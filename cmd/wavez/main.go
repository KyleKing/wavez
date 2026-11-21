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

// exitError is an error that carries the status the process must exit with
// and has already printed whatever it had to say. A hook uses it, because
// the harness reads meaning from the status itself.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

type command struct {
	run     func(ctx context.Context, args []string, streams stdio) error
	name    string
	summary string
}

func commands() []command {
	return []command{
		{name: "init", summary: "wire wavez into this project's Claude Code settings", run: runInit},
		{name: "hook", summary: "handle one Claude Code hook event read from stdin", run: runHook},
		{name: "gate", summary: "run the checks selected by the changed files", run: runGate},
		{name: "rename", summary: "rename a Go symbol and every reference to it", run: runRename},
		{name: "move", summary: "move Go declarations to another file in the package", run: runMove},
		{name: "delete", summary: "delete Go declarations nothing else uses", run: runDelete},
		{name: "bench", summary: "run a pinned task under a harness and record what it cost", run: runBench},
		{name: "stats", summary: "attribute the turns of a recorded Claude Code session", run: runStats},
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
		sayf(streams.out, "wavez %s (%s, %s)\n", version, commit, date)

		return 0
	}

	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}

		return status(c.run(ctx, args[1:], streams), c.name, streams.err)
	}

	sayf(streams.err, "wavez: unknown command %q\n", args[0])
	usage(streams.err)

	return usageStatus
}

func status(err error, name string, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	var code exitError
	if errors.As(err, &code) {
		return int(code)
	}

	sayf(stderr, "wavez %s: %v\n", name, err)

	return 1
}

// usageStatus is the exit status for a command line wavez could not read.
const usageStatus = 2

// sayf writes to a stream whose failure there is nobody left to tell.
func sayf(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return
	}
}

func usage(w io.Writer) {
	sayf(w, "usage: wavez <command> [arguments]\n\n")

	for _, c := range commands() {
		sayf(w, "  %-8s %s\n", c.name, c.summary)
	}
}
