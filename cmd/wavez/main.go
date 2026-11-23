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
	"runtime/debug"
	"strings"
	"syscall"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// The values version, commit, and date hold until a release build sets them.
const (
	unsetVersion = "dev"
	unsetCommit  = "none"
	unsetDate    = "unknown"
)

// versionLine is what `wavez --version` prints. A release build is stamped
// through the linker. A binary from `go install module@version` is not, so
// what the linker left unset is read from the build information Go embeds:
// the module version, and the commit and time when it was built in a
// checkout.
func versionLine(info *debug.BuildInfo) string {
	v, c, d := version, commit, date

	if info != nil {
		if v == unsetVersion && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = strings.TrimPrefix(info.Main.Version, "v")
		}

		for _, setting := range info.Settings {
			switch {
			case setting.Key == "vcs.revision" && c == unsetCommit:
				c = setting.Value
			case setting.Key == "vcs.time" && d == unsetDate:
				d = setting.Value
			}
		}
	}

	return fmt.Sprintf("wavez %s (%s, %s)", v, c, d)
}

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
		{name: "goal", summary: "check the task's written goal with code and say how much of it is done", run: runGoal},
		{name: "route", summary: "say whether a prompt is direct work, exploration, or work to split", run: runRoute},
		{name: "bench", summary: "run a pinned task under a harness and record what it cost", run: runBench},
		{name: "stats", summary: "attribute the turns of a recorded Claude Code session", run: runStats},
		{name: "report", summary: "write a page saying how a session went and what to say next time", run: runReport},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], stdio{in: os.Stdin, out: os.Stdout, err: os.Stderr})

	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, streams stdio) int {
	if len(args) == 0 || asksForHelp(args[0]) {
		usage(streams.out)

		return 0
	}

	if args[0] == "version" || args[0] == "--version" {
		info, _ := debug.ReadBuildInfo()
		sayf(streams.out, "%s\n", versionLine(info))

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

// asksForHelp reports whether an argument is one of the ways to ask a
// command what it does.
func asksForHelp(arg string) bool {
	return arg == "help" || arg == "-h" || arg == "--help"
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
