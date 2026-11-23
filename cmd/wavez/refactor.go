package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/refactor"
)

const (
	renameUsage = "usage: wavez rename [--root DIR] [--dry-run] <file.go|./pkg/dir>:<Name|Type.Member> <NewName>"
	moveUsage   = "usage: wavez move [--root DIR] [--dry-run] <file.go>:<Name>[,<Name>...] <dest.go>"
	deleteUsage = "usage: wavez delete [--root DIR] [--dry-run] <file.go>:<Name>[,<Name>...] [<file.go>:<Name>...]"
)

// refactorUsageError is a command line that does not fit a command's form.
type refactorUsageError struct {
	usage  string
	reason string
}

func (e *refactorUsageError) Error() string { return e.reason + "\n" + e.usage }

// refactorArgs is a refactor command line with its flags taken out of it.
type refactorArgs struct {
	root   string
	rest   []string
	help   bool
	dryRun bool
}

// splitRefactorArgs separates the flags from the positional arguments,
// wherever on the line they were written.
func splitRefactorArgs(args []string, usage string) (refactorArgs, error) {
	var out refactorArgs

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name := strings.TrimLeft(arg, "-")

		switch {
		case !strings.HasPrefix(arg, "-"):
			out.rest = append(out.rest, arg)
		case name == "h" || name == "help":
			out.help = true
		case name == "dry-run" || name == "n":
			out.dryRun = true
		case name == "root" && i+1 < len(args):
			i++
			out.root = args[i]
		case strings.HasPrefix(name, "root="):
			out.root = strings.TrimPrefix(name, "root=")
		default:
			return out, &refactorUsageError{usage: usage, reason: "unknown or incomplete flag " + arg}
		}
	}

	return out, nil
}

// newRefactorer builds the refactorer for the module holding the working
// directory, or for --root when given. Target paths are read relative to the
// working directory first, as a shell user expects.
func newRefactorer(root string, dryRun bool) (*refactor.Refactorer, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("finding the working directory: %w", err)
	}

	if root == "" {
		if root, err = refactor.FindRoot(cwd); err != nil {
			return nil, fmt.Errorf("finding the module root: %w", err)
		}
	}

	r, err := refactor.New(root, refactor.WithWorkDir(cwd), refactor.WithDryRun(dryRun))
	if err != nil {
		return nil, fmt.Errorf("opening the module: %w", err)
	}

	return r, nil
}

// summarizer is the result of a refactor: the report it prints.
type summarizer interface {
	Summary() string
}

// refactorCommand parses a refactor command line and hands its positional
// arguments to run, whose result is the report to print. A refusal is
// returned as the refactor worded it: main already prefixes the command.
func refactorCommand(
	args []string, streams stdio, usage string, valid func(n int) bool,
	run func(r *refactor.Refactorer, rest []string) (summarizer, error),
) error {
	parsed, err := splitRefactorArgs(args, usage)
	if err != nil {
		return err
	}

	if parsed.help {
		return report(streams, usage+"\n")
	}

	if !valid(len(parsed.rest)) {
		return &refactorUsageError{usage: usage, reason: fmt.Sprintf("got %d arguments", len(parsed.rest))}
	}

	r, err := newRefactorer(parsed.root, parsed.dryRun)
	if err != nil {
		return err
	}

	res, err := run(r, parsed.rest)
	if err != nil {
		return err
	}

	return report(streams, res.Summary())
}

func report(streams stdio, text string) error {
	if _, err := fmt.Fprint(streams.out, text); err != nil {
		return fmt.Errorf("writing the report: %w", err)
	}

	return nil
}

func runRename(ctx context.Context, args []string, streams stdio) error {
	return refactorCommand(args, streams, renameUsage, func(n int) bool { return n == 2 }, //nolint:mnd // target, name
		func(r *refactor.Refactorer, rest []string) (summarizer, error) {
			return r.Rename(ctx, rest[0], rest[1])
		})
}

func runMove(ctx context.Context, args []string, streams stdio) error {
	return refactorCommand(args, streams, moveUsage, func(n int) bool { return n == 2 }, //nolint:mnd // target, dest
		func(r *refactor.Refactorer, rest []string) (summarizer, error) {
			return r.Move(ctx, rest[0], rest[1])
		})
}

func runDelete(ctx context.Context, args []string, streams stdio) error {
	return refactorCommand(args, streams, deleteUsage, func(n int) bool { return n >= 1 },
		func(r *refactor.Refactorer, rest []string) (summarizer, error) {
			return r.Delete(ctx, rest)
		})
}
