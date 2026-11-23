package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/route"
)

const routeUsage = `usage:
  wavez route [--dir DIR] <prompt text...>
      print the route a prompt gets, and the hint the agent would see
  wavez route [--dir DIR] eval <corpus.jsonl>
      run a labeled corpus through the rules, then the configured model
  wavez route eval --claude MODEL <corpus.jsonl>
      run it through the rules, then ask claude -p --model MODEL about each
      case the rules leave. Every one of those is a billed request.`

const routeEval = "eval"

// claudeParallel is how many headless claude runs are in flight at once.
const claudeParallel = 4

// runRoute answers the route question by hand: for one prompt, or for a
// labeled corpus. It reads the project's configuration but not its route
// switch, so a project can look at the answers before turning the hint on.
// Nothing it decides is written to the decision log, which holds what the
// hook saw and is the next corpus.
func runRoute(ctx context.Context, args []string, streams stdio) error {
	flags := flag.NewFlagSet("wavez route", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	dir := flags.String("dir", ".", "project directory whose configuration and paths are read")

	if err := flags.Parse(args); err != nil || flags.NArg() == 0 {
		return &usageError{msg: routeUsage}
	}

	args = flags.Args()

	root := hook.FindRoot(*dir)

	cfg, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	if args[0] == routeEval {
		return routeCorpus(ctx, cfg.Decide, args[1:], streams)
	}

	got := route.Decide(ctx, decide.Configured(cfg.Decide), root, strings.Join(args, " "))

	hint := route.Hint(got.Choice)
	if hint == "" {
		hint = "(none: the agent is told nothing)"
	}

	text := fmt.Sprintf("choice   %s\nbackend  %s\n", got.Choice, got.Backend)
	if got.Reason != "" {
		text += fmt.Sprintf("reason   %s\n", got.Reason)
	}

	return report(streams, text+fmt.Sprintf("hint     %s\n", hint))
}

// routeCorpus prints how the rules and a model did on a labeled corpus.
// Each line's state is used as written, existing-paths line included, so
// the result does not depend on the directory it is run from. With --claude
// the model is a headless claude, and how many requests that will bill is
// printed before the first is made.
func routeCorpus(ctx context.Context, cfg config.Decide, args []string, streams stdio) error {
	flags := flag.NewFlagSet("wavez route eval", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	claude := flags.String("claude", "", "ask claude -p with this model about what the rules leave")

	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return &usageError{msg: routeUsage}
	}

	path := flags.Arg(0)

	// The corpus is read as one named file of its directory.
	body, err := fs.ReadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
	if err != nil {
		return fmt.Errorf("opening the corpus: %w", err)
	}

	cases, err := decide.ReadCorpus(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	decider, parallel := decide.Configured(cfg), 1

	if *claude != "" {
		rulesOnly := decide.Evaluate(ctx, decide.New(), route.Question(), route.Rules(), cases, 1)

		notice := fmt.Sprintf("asking claude (%s) about the %d of %d cases the rules leave, %d at a time\n",
			*claude, rulesOnly.Left(), len(cases), claudeParallel)
		if err := report(streams, notice); err != nil {
			return err
		}

		decider, parallel = decide.New(decide.WithBackend(decide.NewClaude(*claude))), claudeParallel
	}

	return report(streams, decide.Evaluate(ctx, decider, route.Question(), route.Rules(), cases, parallel).Render())
}
