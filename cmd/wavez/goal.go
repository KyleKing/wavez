package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/hook"
)

const goalUsage = `usage:
  wavez goal [status] [--json]                         check every condition and say how much is done
  wavez goal init "<sentence>" [--block] [--force]     write a starter .wavez/goal.toml
  wavez goal add test "./pkg -run TestName" [--name N] [--timeout D]
  wavez goal add grep <file> <pattern> [--name N]      the pattern is found in the file
  wavez goal add absent <glob> <pattern> [--name N]    the pattern is found in no matching file
  wavez goal add file <path> [--name N]                the path exists
  wavez goal add command "<shell command>" [--name N] [--timeout D]
  wavez goal clear                                     remove the goal and what was cached about it
every form takes --dir DIR, and words after -- are never read as flags`

// goalFlags are the flags of every goal subcommand. They are parsed as one
// set so that a flag may follow the words it qualifies, the way
// `add test "./pkg" --name "it passes"` reads.
type goalFlags struct {
	dir     string
	name    string
	timeout string
	asJSON  bool
	block   bool
	force   bool
}

// parseGoal separates the flags from the words around them.
func parseGoal(args []string) (goalFlags, []string, error) {
	var opts goalFlags

	flags := flag.NewFlagSet("wavez goal", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.dir, "dir", ".", "project directory")
	flags.StringVar(&opts.name, "name", "", "the condition's name")
	flags.StringVar(&opts.timeout, "timeout", "", "how long a test or command may run")
	flags.BoolVar(&opts.asJSON, "json", false, "print the report as JSON")
	flags.BoolVar(&opts.block, "block", false, "hold the end of the turn while the goal is unmet")
	flags.BoolVar(&opts.force, "force", false, "replace an existing goal")

	var words, literal []string

	if at := slices.Index(args, "--"); at >= 0 {
		args, literal = args[:at], args[at+1:]
	}

	for len(args) > 0 {
		if err := flags.Parse(args); err != nil {
			return opts, nil, &usageError{msg: err.Error() + "\n" + goalUsage}
		}

		args = flags.Args()
		if len(args) > 0 {
			words, args = append(words, args[0]), args[1:]
		}
	}

	return opts, append(words, literal...), nil
}

// runGoal reads, writes, and checks the project's goal. Checking exits 1
// when a condition is not done, after the report is printed.
func runGoal(ctx context.Context, args []string, streams stdio) error {
	opts, words, err := parseGoal(args)
	if err != nil {
		return err
	}

	root := hook.FindRoot(opts.dir)

	if len(words) == 0 {
		words = []string{"status"}
	}

	switch {
	case words[0] == "status" && len(words) == 1:
		return goalStatus(ctx, root, opts, streams)
	case words[0] == "init" && len(words) == 2:
		return goalInit(root, words[1], opts, streams)
	case words[0] == "add" && len(words) > 1:
		return goalAdd(root, words[1:], opts, streams)
	case words[0] == "clear" && len(words) == 1:
		return goalClear(root, streams)
	default:
		return &usageError{msg: goalUsage}
	}
}

func goalStatus(ctx context.Context, root string, opts goalFlags, streams stdio) error {
	loaded, err := goal.Load(root)
	if errors.Is(err, goal.ErrNoGoal) {
		if opts.asJSON {
			return report(streams, "{}\n")
		}

		return report(streams, "no goal is set: start one with `wavez goal init \"<sentence>\"`\n")
	}

	if err != nil {
		return fmt.Errorf("loading the goal: %w", err)
	}

	result, err := goal.Evaluate(ctx, root, loaded, goal.Options{Expensive: true})
	if err != nil {
		sayf(streams.err, "wavez goal: %v\n", err)
	}

	summary := result.Summary()
	text := result.Render()

	if len(summary.Unmet) == 0 && summary.CouldNotCheck > 0 {
		text += "nothing is known to be unmet: a condition that could not be checked is not a failure\n"
	}

	if opts.asJSON {
		text, err = goalJSON(result, summary)
		if err != nil {
			return err
		}
	}

	if err := report(streams, text); err != nil {
		return err
	}

	if len(summary.Unmet) > 0 {
		return exitError(1)
	}

	return nil
}

func goalJSON(result goal.Report, summary goal.Summary) (string, error) {
	encoded, err := json.MarshalIndent(struct {
		goal.Report

		Summary goal.Summary `json:"summary"`
	}{result, summary}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding the report: %w", err)
	}

	return string(encoded) + "\n", nil
}

func goalInit(root, sentence string, opts goalFlags, streams stdio) error {
	file, err := goal.Init(root, sentence, opts.block, opts.force)
	if errors.Is(err, goal.ErrExists) {
		return fmt.Errorf("%w; --force replaces it", err)
	}

	if err != nil {
		return fmt.Errorf("writing the goal: %w", err)
	}

	return report(streams, "wrote "+shown(root, file)+"\nadd a condition with `wavez goal add <kind> ...`\n")
}

func goalAdd(root string, words []string, opts goalFlags, streams stdio) error {
	item, err := goal.Add(root, goal.Addition{
		Kind: goal.Kind(words[0]), Name: opts.name, Timeout: opts.timeout, Args: words[1:],
	})
	if errors.Is(err, goal.ErrAddition) {
		return &usageError{msg: err.Error() + "\n" + goalUsage}
	}

	if err != nil {
		return fmt.Errorf("adding the condition: %w", err)
	}

	return report(streams, fmt.Sprintf("added %s %q\n", item.Kind, item.Name))
}

func goalClear(root string, streams stdio) error {
	had, err := goal.Clear(root)
	if err != nil {
		return fmt.Errorf("clearing the goal: %w", err)
	}

	if !had {
		return report(streams, "no goal was set\n")
	}

	return report(streams, "cleared the goal\n")
}

// shown is path relative to root, the way the person would type it.
func shown(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}

	return path
}
