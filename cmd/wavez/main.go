// Package main is the wavez CLI. `wavez -p "…"` runs one prompt headless and
// prints the result; without -p it prints usage until the TUI lands.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
)

var (
	errStoppedEarly = errors.New("thread stopped early")
	errUnknownModel = errors.New("unknown -model: want local or hosted")
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type options struct {
	prompt   string
	dir      string
	model    string
	with     string
	allowAll bool
	maxTurns int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "wavez: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("wavez", flag.ContinueOnError)
	fs.Usage = func() { printHelp(fs.Output()) }

	var (
		opt         options
		showVersion bool
	)
	fs.StringVar(&opt.prompt, "p", "", "run one prompt headless and print the result")
	fs.StringVar(&opt.dir, "dir", "", "project root (defaults to the enclosing repo, then cwd)")
	fs.StringVar(&opt.model, "model", "", "force a tier for every turn: local or hosted")
	fs.StringVar(&opt.with, "with", "", "add one file to the stable prefix for this run only")
	fs.BoolVar(&opt.allowAll, "allow-all", false, "approve every permission prompt without asking")
	fs.IntVar(&opt.maxTurns, "max-turns", 0, "cap model turns (0 uses the loop default)")
	fs.BoolVar(&showVersion, "v", false, "print version information")

	if err := fs.Parse(args); err != nil {
		return err //nolint:wrapcheck // flag already prints the reason and usage
	}
	if showVersion {
		fmt.Printf("wavez %s (commit: %s, built: %s)\n", version, commit, date)

		return nil
	}
	if opt.prompt == "" {
		printHelp(os.Stdout)

		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return headless(ctx, opt)
}

func headless(ctx context.Context, opt options) error {
	root, err := resolveRoot(ctx, opt.dir)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(ctx, root, opt.with)
	if err != nil {
		return err
	}

	a, err := app.New(ctx, root, cfg, permissionGate(opt.allowAll), app.WithAsker(stdinAsker{}))
	if err != nil {
		return fmt.Errorf("building project: %w", err)
	}
	defer func() {
		if cerr := a.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavez: shutdown: %v\n", cerr)
		}
	}()

	th, err := a.OpenThread(app.DefaultThreadID, append([]string{root}, cfg.ExtraDirs...))
	if err != nil {
		return fmt.Errorf("opening thread: %w", err)
	}

	hint, err := routerHint(opt.model)
	if err != nil {
		return err
	}

	loop := a.Loop
	if opt.maxTurns > 0 {
		loop = agent.New(a.Local, a.Hosted, a.Tools, a.Permission, agent.WithMaxTurns(opt.maxTurns))
	}

	outcome, err := loop.Run(ctx, th, prefix(a), opt.prompt, hint)
	if err != nil {
		return fmt.Errorf("running thread: %w", err)
	}

	fmt.Println(finalText(th))
	fmt.Fprintf(os.Stderr, "\nstop=%s turns=%d tool_calls=%d\n", outcome.Stop, outcome.Turns, outcome.ToolCalls)

	if outcome.Stop != agent.StopComplete {
		return fmt.Errorf("%w: %s", errStoppedEarly, outcome.Stop)
	}

	return nil
}

func prefix(a *app.App) agent.Prefix {
	specs := a.Tools.Specs()
	out := make([]llm.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, llm.ToolSpec{Name: s.Name, Description: s.Description, Schema: s.Schema})
	}

	return agent.Prefix{System: a.SystemPrefix, Tools: out}
}

func loadConfig(ctx context.Context, root, with string) (config.Config, error) {
	loader, err := config.NewLoader(ctx)
	if err != nil {
		return config.Config{}, fmt.Errorf("starting config loader: %w", err)
	}
	defer func() { _ = loader.Close() }() //nolint:errcheck // the evaluator is a child process we are done with

	var opts []config.Option
	if with != "" {
		opts = append(opts, config.WithExtra(with))
	}

	cfg, _, err := loader.Load(ctx, root, opts...)
	if err != nil {
		return config.Config{}, fmt.Errorf("loading config: %w", err)
	}

	return cfg, nil
}

func routerHint(model string) (router.Input, error) {
	switch strings.ToLower(model) {
	case "":
		return router.Input{}, nil
	case "local":
		return router.Input{Override: router.ChoiceLocal}, nil
	case "hosted":
		return router.Input{Override: router.ChoiceHosted}, nil
	default:
		return router.Input{}, fmt.Errorf("%w: %q", errUnknownModel, model)
	}
}

// permissionGate asks on the terminal unless the caller opted out. Approval
// never comes from model output, only from this prompt or the explicit flag.
//
//nolint:ireturn // a Gate is exactly what the app expects to be handed
func permissionGate(allowAll bool) permission.Gate {
	if allowAll {
		return permission.AllowAll()
	}

	reader := bufio.NewReader(os.Stdin)

	return permission.GateFunc(func(_ context.Context, req permission.Request) (permission.Decision, error) {
		fmt.Fprintf(os.Stderr, "\nallow? %s %s\n", req.Tool, req.Action)
		if req.Detail != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", req.Detail)
		}
		fmt.Fprint(os.Stderr, "  [y]es [n]o [a]lways: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return permission.Deny, nil //nolint:nilerr // no answer means no approval
		}
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "y", "yes":
			return permission.Allow, nil
		case "a", "always":
			return permission.AllowAlways, nil
		default:
			return permission.Deny, nil
		}
	})
}

type stdinAsker struct{}

func (stdinAsker) Ask(_ context.Context, question string) (string, error) {
	fmt.Fprintf(os.Stderr, "\n%s\n> ", question)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading answer: %w", err)
	}

	return strings.TrimSpace(line), nil
}

func finalText(th *thread.Thread) string {
	history := th.History()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleAssistant && strings.TrimSpace(history[i].Content) != "" {
			return history[i].Content
		}
	}

	return ""
}

func resolveRoot(ctx context.Context, dir string) (string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolving -dir: %w", err)
		}

		return abs, nil
	}

	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolving cwd: %w", err)
	}

	return cwd, nil
}

func printHelp(w io.Writer) {
	//nolint:errcheck // best-effort usage output
	fmt.Fprint(w, `wavez - a personal AI coding agent

Usage:
  wavez -p "make the lease TTL configurable"

Flags:
  -p <prompt>     run one prompt headless and print the result
  -dir <path>     project root (defaults to the enclosing repo, then cwd)
  -model <tier>   force local or hosted for every turn
  -with <file>    add one file to the stable prefix for this run only
  -allow-all      approve every permission prompt without asking
  -max-turns <n>  cap model turns
  -v              print version information

The TUI is not wired yet; -p is the only entry point.
`)
}
