package main

import (
	"bytes"
	"context"
	"fmt"
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
  wavez route <prompt text...>      print the route a prompt gets, and the hint the agent would see
  wavez route eval <corpus.jsonl>   run a labeled corpus through the rules and the configured model`

const routeEval = "eval"

// runRoute answers the route question by hand: for one prompt, or for a
// labeled corpus. It reads the project's configuration but not its route
// switch, so a project can look at the answers before turning the hint on.
func runRoute(ctx context.Context, args []string, streams stdio) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		return &usageError{msg: routeUsage}
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding the working directory: %w", err)
	}

	root := hook.FindRoot(dir)

	cfg, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	decider := decide.FromConfig(root, cfg.Decide)

	if args[0] == routeEval {
		return routeCorpus(ctx, decider, args[1:], streams)
	}

	got := route.Decide(ctx, decider, root, strings.Join(args, " "))

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

// routeCorpus prints how the rules and the model did on a labeled corpus.
// Each line's state is used as written, existing-paths line included, so
// the result does not depend on the directory it is run from.
func routeCorpus(ctx context.Context, decider decide.Decider, args []string, streams stdio) error {
	if len(args) != 1 {
		return &usageError{msg: routeUsage}
	}

	// The corpus is read as one named file of its directory.
	body, err := fs.ReadFile(os.DirFS(filepath.Dir(args[0])), filepath.Base(args[0]))
	if err != nil {
		return fmt.Errorf("opening the corpus: %w", err)
	}

	cases, err := decide.ReadCorpus(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("reading %s: %w", args[0], err)
	}

	return report(streams, decide.Evaluate(ctx, decider, route.Question(), route.Rules(), cases).Render())
}
