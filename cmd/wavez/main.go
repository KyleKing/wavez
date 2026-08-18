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
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tui"
	"github.com/kyleking/wavez/internal/vcs"
)

var (
	errNothingToUndo     = errors.New("nothing has changed since the checkpoint")
	errRestoreIncomplete = errors.New("restore left the working copy changed")
	errStoppedEarly      = errors.New("thread stopped early")
	errUnreachableCode   = errors.New("unreachable code found")
	errUnknownModel      = errors.New("unknown -model: want local or hosted")
	errCycleStopped      = errors.New("cycle stopped")
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type options struct {
	prompt              string
	cycle               string
	dir                 string
	model               string
	with                string
	resume              string
	socket              string
	undo                string
	maxTurns            int
	maxToolCallsPerTurn int
	maxStagnantErrors   int
	maxWallClock        time.Duration
	maxHostedSpendUSD   float64
	allowAll            bool
	strictScope         bool
	mutate              bool
	jsonOut             bool
	plan                bool
	deadcode            bool
}

func main() {
	slog.SetDefault(slog.New(lsp.Quiet(slog.NewTextHandler(os.Stderr, nil))))

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
	fs.StringVar(&opt.cycle, "cycle", "",
		"run the prompt through a named cycle (e.g. fix) instead of one loop")
	fs.StringVar(&opt.dir, "dir", "", "project root (defaults to the enclosing repo, then cwd)")
	fs.StringVar(&opt.model, "model", "", "force a tier for every turn: local or hosted")
	fs.StringVar(&opt.with, "with", "", "add one file to the stable prefix for this run only")
	fs.StringVar(&opt.socket, "socket", "", "daemon socket path (defaults to <root>/.wavez/d.sock)")
	fs.StringVar(&opt.resume, "resume", "", "continue an existing thread by id instead of starting a new one")
	fs.StringVar(&opt.undo, "undo", "",
		"restore the working copy to a run's checkpoint operation id, discarding everything since")
	fs.BoolVar(&opt.allowAll, "allow-all", false, "approve every permission prompt without asking")
	fs.BoolVar(&opt.strictScope, "strict-scope", false,
		"refuse an edit to a file this run never read or created")
	fs.IntVar(&opt.maxTurns, "max-turns", 0, "cap model turns, a dead-man's switch (0 uses the loop default)")
	fs.IntVar(&opt.maxToolCallsPerTurn, "max-tool-calls-per-turn", 0,
		"cap tool calls within one model turn (0 uses the loop default)")
	fs.IntVar(&opt.maxStagnantErrors, "max-stagnant-errors", 0,
		"cap consecutive erroring tool-call results (0 uses the loop default)")
	fs.DurationVar(&opt.maxWallClock, "max-wall-clock", 0, "cap one run's wall time (0 uses the loop default)")
	fs.Float64Var(&opt.maxHostedSpendUSD, "max-hosted-spend", 0,
		"cap one run's hosted-tier spend in dollars (0 uses the loop default)")
	fs.BoolVar(&opt.plan, "plan", false,
		"run with read-only tools, so the thread can investigate but not edit")
	fs.BoolVar(&opt.jsonOut, "json", false,
		"with -p, print one JSON object on stdout instead of the result text")
	fs.BoolVar(&opt.deadcode, "deadcode", false,
		"report functions no main reaches, an orphan check the compiler cannot do")
	fs.BoolVar(&opt.mutate, "mutate", false,
		"mutate the working copy's changed lines and report the mutants the tests missed")
	fs.BoolVar(&showVersion, "v", false, "print version information")

	if err := fs.Parse(args); err != nil {
		return err //nolint:wrapcheck // flag already prints the reason and usage
	}
	if showVersion {
		fmt.Printf("wavez %s (commit: %s, built: %s)\n", version, commit, date)

		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if handled, err := runSubcommand(ctx, opt); handled {
		return err
	}

	if opt.prompt == "" {
		return launchTUI(ctx, opt)
	}

	return headless(ctx, opt)
}

// launchTUI attaches to a running wavezd. The daemon is started separately so
// a client crash never takes the threads with it.
func launchTUI(ctx context.Context, opt options) error {
	root, err := resolveRoot(ctx, opt.dir)
	if err != nil {
		return err
	}

	sock := opt.socket
	if sock == "" {
		sock = filepath.Join(root, ".wavez", "d.sock")
	}

	client, err := api.Dial(ctx, sock)
	if err != nil {
		return fmt.Errorf("no daemon at %s: %w (start one with `wavezd`)", sock, err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavez: closing connection: %v\n", cerr)
		}
	}()

	if err := tui.Run(ctx, client, tui.Options{Dir: root, NoColor: os.Getenv("NO_COLOR") != ""}); err != nil {
		return fmt.Errorf("running the interface: %w", err)
	}

	return nil
}

// appOptions maps the flags a headless run was given to app.Option values.
// A zero bound means "leave the default", never "no bound".
func appOptions(opt options) []app.Option {
	out := []app.Option{app.WithAsker(stdinAsker{}), app.WithManagedLocalServer()}

	if opt.maxTurns > 0 {
		out = append(out, app.WithMaxTurns(opt.maxTurns))
	}

	if opt.maxToolCallsPerTurn > 0 {
		out = append(out, app.WithMaxToolCallsPerTurn(opt.maxToolCallsPerTurn))
	}

	if opt.maxStagnantErrors > 0 {
		out = append(out, app.WithMaxStagnantErrors(opt.maxStagnantErrors))
	}

	if opt.maxWallClock > 0 {
		out = append(out, app.WithMaxWallClock(opt.maxWallClock))
	}

	if opt.maxHostedSpendUSD > 0 {
		out = append(out, app.WithMaxHostedSpendUSD(opt.maxHostedSpendUSD))
	}

	if opt.strictScope {
		out = append(out, app.WithStrictScope())
	}

	return out
}

// runSubcommand dispatches the flags that do one job and exit instead of
// opening a thread. It reports handled=false when none applies.
func runSubcommand(ctx context.Context, opt options) (bool, error) {
	if opt.undo == "" && !opt.deadcode && !opt.mutate {
		return false, nil
	}

	root, err := resolveRoot(ctx, opt.dir)
	if err != nil {
		return true, err
	}

	switch {
	case opt.undo != "":
		return true, undo(ctx, root, opt.undo)
	case opt.deadcode:
		cfg, cerr := loadConfig(ctx, root, opt.with)
		if cerr != nil {
			return true, cerr
		}

		return true, deadcodeCheck(ctx, root, cfg)
	default:
		return true, mutationCheck(ctx, root)
	}
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

	a, err := app.New(ctx, root, cfg, permissionGate(opt.allowAll), appOptions(opt)...)
	if err != nil {
		return fmt.Errorf("building project: %w", err)
	}
	// Close takes no context on purpose: a run canceled by ctrl-c must
	// still stop the llama-server it started, and a canceled context would
	// skip exactly that.
	//nolint:contextcheck // see the comment above: shutdown must outlive the run's context
	defer func() {
		if cerr := a.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavez: shutdown: %v\n", cerr)
		}
	}()

	th, err := a.OpenThread(threadID(opt.resume), append([]string{root}, cfg.ExtraDirs...))
	if err != nil {
		return fmt.Errorf("opening thread: %w", err)
	}
	fmt.Fprintf(os.Stderr, "thread %s\n", th.ID())

	hint, err := routerHint(opt.model)
	if err != nil {
		return err
	}

	prompt, err := expandMentions(ctx, a, opt.prompt)
	if err != nil {
		return err
	}

	if opt.cycle != "" {
		return runCycle(ctx, a, th, prompt, opt, hint)
	}

	loop, tools, system := a.Loop, a.Tools, a.SystemPrefix
	if opt.plan {
		loop, tools, system = a.PlanLoop, a.PlanTools, a.PlanSystem
	}

	outcome, err := loop.Run(ctx, th, prefix(system, tools), prompt, hint)
	if err != nil {
		return fmt.Errorf("running thread: %w", err)
	}

	if err := reportRun(th, a, outcome, opt, root); err != nil {
		return err
	}

	if outcome.Stop != agent.StopComplete {
		return fmt.Errorf("%w: %s", errStoppedEarly, outcome.Stop)
	}

	return nil
}

// runCycle drives the named cycle instead of one loop. A cycle whose last
// phase's Condition did not hold exits nonzero carrying that reason: work
// the harness refused to advance is not work that finished.
func runCycle(
	ctx context.Context, a *app.App, th *thread.Thread, prompt string, opt options, hint router.Input,
) error {
	c, err := a.Cycle(opt.cycle)
	if err != nil {
		return fmt.Errorf("running a cycle: %w", err)
	}

	driver := a.CycleDriver(th.ID(), append([]string{a.Root}, a.Config.ExtraDirs...), hint)

	outcome, err := cycle.NewRunner(a.Root, driver, th.Log()).Run(ctx, c, prompt)
	if err != nil {
		return fmt.Errorf("running cycle %s: %w", opt.cycle, err)
	}

	reportCycle(outcome, opt)

	if outcome.Stop != cycle.StopComplete {
		return fmt.Errorf("%w: %s in %s: %s", errCycleStopped, outcome.Stop, outcome.Phase, outcome.Verdict.Reason)
	}

	return nil
}

// reportCycle prints one cycle's outcome: which phase it reached, what the
// harness observed there, and what every phase cost.
func reportCycle(outcome cycle.Outcome, opt options) {
	if opt.jsonOut {
		if err := writeJSON(os.Stdout, newCycleResult(outcome)); err != nil {
			fmt.Fprintf(os.Stderr, "wavez: %v\n", err)
		}

		return
	}

	for _, p := range outcome.Phases {
		fmt.Fprintf(os.Stderr, "%-12s %d attempt(s)  %-24s %s\n",
			p.Phase, p.Attempts, p.Verdict.Condition, p.Verdict.Reason)
	}

	fmt.Fprintf(os.Stderr, "\nstop=%s phase=%s turns=%d tool_calls=%d hosted_spend=$%.4f\n",
		outcome.Stop, outcome.Phase, outcome.Turns, outcome.ToolCalls, outcome.SpendUSD)
}

// undo restores root to a checkpoint an earlier run captured, which the
// run printed as checkpoint=<id>. Typing the flag is the confirmation the
// TUI asks for, so this acts directly, and it prints the work it destroyed
// rather than only that it ran.
//
// Nothing to discard is a refusal: jj's own restore is silent about a
// no-op, and an undo that reports success without undoing anything is
// worse than one that fails.
func undo(ctx context.Context, root, checkpoint string) error {
	jj := vcs.NewJj()

	changed, err := jj.ChangedFiles(ctx, root, checkpoint)
	if err != nil {
		return fmt.Errorf("listing what -undo would discard: %w", err)
	}
	if len(changed) == 0 {
		return fmt.Errorf("%w: %s", errNothingToUndo, checkpoint)
	}

	stat, err := jj.DiffStat(ctx, root, checkpoint)
	if err != nil {
		return fmt.Errorf("summarizing what -undo would discard: %w", err)
	}

	if err := jj.Restore(ctx, root, checkpoint); err != nil {
		return fmt.Errorf("restoring %s: %w", root, err)
	}

	left, err := jj.ChangedFiles(ctx, root, checkpoint)
	if err != nil {
		return fmt.Errorf("verifying the restore of %s: %w", root, err)
	}
	if len(left) > 0 {
		return fmt.Errorf("%w: %d file(s) still differ", errRestoreIncomplete, len(left))
	}

	fmt.Printf("restored %s to checkpoint %s, discarding:\n%s", root, checkpoint, stat)

	return nil
}

// threadID starts a fresh thread unless the caller resumes one by name. A
// shared id would carry an unrelated run's history into this one's prefix,
// which costs tokens on every turn and contaminates the answer.
func threadID(resume string) thread.ID {
	if resume != "" {
		return thread.ID(resume)
	}

	return thread.ID("p-" + strconv.FormatInt(time.Now().UnixNano(), 36))
}

func prefix(system string, tools *tool.Registry) agent.Prefix {
	specs := tools.Specs()
	out := make([]llm.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, llm.ToolSpec{Name: s.Name, Description: s.Description, Schema: s.Schema})
	}

	return agent.Prefix{System: system, Tools: out}
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
		if req.Reason != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", req.Reason)
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
  -cycle <name>   run the prompt through a named cycle (fix ships built in)
  -json           with -p, print one JSON object on stdout instead of the text
  -plan           run with read-only tools: investigate without editing
  -dir <path>     project root (defaults to the enclosing repo, then cwd)
  -model <tier>   force local or hosted for every turn
  -with <file>    add one file to the stable prefix for this run only
  -resume <id>    continue an existing thread instead of starting a new one
  -undo <op>      restore the working copy to a run's checkpoint and print what it discarded
  -socket <path>  daemon socket path (defaults to <root>/.wavez/d.sock)
  -allow-all      approve every permission prompt without asking
  -strict-scope   refuse an edit to a file this run never read or created
  -mutate         mutate the working copy's changed lines and report what the tests missed
  -deadcode       report functions no main reaches, then exit nonzero if any are unexpected
  -max-turns <n>                cap model turns, a dead-man's switch
  -max-tool-calls-per-turn <n>  cap tool calls within one model turn
  -max-stagnant-errors <n>      cap consecutive erroring tool-call results
  -max-wall-clock <duration>    cap one run's wall time (e.g. 180s)
  -max-hosted-spend <dollars>   cap one run's hosted-tier spend
  -v              print version information

With no -p, wavez attaches to a running wavezd and opens the interface.
`)
}

// expandMentions resolves @file and @symbol references before the prompt
// reaches the model. An unresolved mention stays literal in the prompt and
// is named on stderr, since silently dropping a reference leaves both the
// user and the model to guess why nothing arrived. The notice goes to
// stderr so -json keeps stdout to one object.
func expandMentions(ctx context.Context, a *app.App, prompt string) (string, error) {
	res, err := a.Mentions.Expand(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("expanding mentions: %w", err)
	}

	for _, m := range res.Unresolved() {
		fmt.Fprintf(os.Stderr, "wavez: @%s did not resolve: %s\n", m.Ref, m.Detail)
	}

	return res.Prompt, nil
}

// reportRun prints one run's outcome, as a JSON object on stdout under
// -json and as the result text plus a human summary on stderr otherwise.
func reportRun(th *thread.Thread, a *app.App, outcome agent.Outcome, opt options, root string) error {
	if opt.jsonOut {
		return writeJSON(os.Stdout, newRunResult(th.ID(), finalText(th), outcome,
			relStrayed(a.Scope.Strayed(), root)))
	}

	fmt.Println(finalText(th))
	fmt.Fprintf(os.Stderr, "\nstop=%s elapsed=%s turns=%d tool_calls=%d hosted_spend=$%.4f checkpoint=%s\n",
		outcome.Stop, outcome.Elapsed.Round(time.Second), outcome.Turns, outcome.ToolCalls,
		outcome.HostedSpendUSD, outcome.Checkpoint)
	reportStrayedEdits(a.Scope.Strayed(), root, opt.strictScope)

	return nil
}

// Strayed paths are absolute; a report shows them relative to root, leaving
// any path outside it absolute.
func relStrayed(strayed []string, root string) []string {
	if len(strayed) == 0 {
		return nil
	}

	out := make([]string, len(strayed))
	for i, abs := range strayed {
		out[i] = abs
		if rel, err := filepath.Rel(root, abs); err == nil {
			out[i] = rel
		}
	}

	return out
}

// reportStrayedEdits names the files a run reached for without ever reading
// or creating them. It prints nothing on a clean run, so the line only
// appears when it says something.
func reportStrayedEdits(strayed []string, root string, strict bool) {
	if len(strayed) == 0 {
		return
	}

	verb := "edited without reading first"
	if strict {
		verb = "refused, never read by this run"
	}

	fmt.Fprintf(os.Stderr, "%s (%d):\n", verb, len(strayed))

	for _, abs := range strayed {
		path := abs
		if rel, err := filepath.Rel(root, abs); err == nil {
			path = rel
		}

		fmt.Fprintf(os.Stderr, "  %s\n", path)
	}
}
