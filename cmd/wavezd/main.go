// Package main is the wavez daemon. It owns the threads, the agent loop, and
// the project's stores, and serves every client over one unix socket.
package main

import (
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
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/sysinfo"
	"github.com/kyleking/wavez/internal/vcs"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "wavezd: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("wavezd", flag.ContinueOnError)
	fs.Usage = func() { printHelp(fs.Output()) }

	var (
		dir         string
		sock        string
		showVersion bool
	)
	fs.StringVar(&dir, "dir", "", "project root (defaults to the enclosing repo, then cwd)")
	fs.StringVar(&sock, "socket", "", "unix socket path (defaults to <root>/.wavez/d.sock)")
	fs.BoolVar(&showVersion, "v", false, "print version information")

	if err := fs.Parse(args); err != nil {
		return err //nolint:wrapcheck // flag already prints the reason and usage
	}
	if showVersion {
		fmt.Printf("wavezd %s (commit: %s, built: %s)\n", version, commit, date)

		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serve(ctx, dir, sock)
}

func serve(ctx context.Context, dir, sock string) error {
	root, err := resolveRoot(ctx, dir)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(ctx, root)
	if err != nil {
		return err
	}

	broker := daemon.NewBroker()

	a, err := app.New(ctx, root, cfg, broker.Gate(), app.WithAsker(broker.Asker()), app.WithManagedLocalServer())
	if err != nil {
		return fmt.Errorf("building project: %w", err)
	}
	// Close takes no context on purpose: a run canceled by ctrl-c must
	// still stop the llama-server it started, and a canceled context would
	// skip exactly that.
	//nolint:contextcheck // see the comment above: shutdown must outlive the run's context
	defer func() {
		if cerr := a.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavezd: shutdown: %v\n", cerr)
		}
	}()

	if sock == "" {
		sock = filepath.Join(root, ".wavez", "d.sock")
	}

	srv, err := daemon.New(sock,
		daemon.WithLoop(a.Loop),
		daemon.WithBroker(broker),
		daemon.WithLogDir(filepath.Join(root, ".wavez", "threads")),
		daemon.WithPrefix(prefix(a)),
		daemon.WithStatsSource(machineStats{ctx: ctx}),
		daemon.WithDiffer(vcs.NewJj()),
		daemon.WithRestorer(vcs.NewJj()),
		daemon.WithRoot(root),
	)
	if err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	fmt.Fprintf(os.Stderr, "wavezd listening on %s\n", sock)

	if err := srv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serving: %w", err)
	}

	return nil
}

// machineStats reads real memory for the diagnostics strip. A zeroed reading
// is reported as zero rather than guessed, so the panel never invents a number.
type machineStats struct {
	ctx context.Context //nolint:containedctx // StatsSource.Stats takes no context
}

func (m machineStats) Stats() daemon.MemStats {
	mem, err := sysinfo.ReadMemory(m.ctx)
	if err != nil {
		return daemon.MemStats{}
	}

	return daemon.MemStats{UsedBytes: mem.UsedBytes, TotalBytes: mem.TotalBytes}
}

func prefix(a *app.App) agent.Prefix {
	specs := a.Tools.Specs()
	out := make([]llm.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, llm.ToolSpec{Name: s.Name, Description: s.Description, Schema: s.Schema})
	}

	return agent.Prefix{System: a.SystemPrefix, Tools: out}
}

func loadConfig(ctx context.Context, root string) (config.Config, error) {
	loader, err := config.NewLoader(ctx)
	if err != nil {
		return config.Config{}, fmt.Errorf("starting config loader: %w", err)
	}
	defer func() {
		if cerr := loader.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavezd: closing config loader: %v\n", cerr)
		}
	}()

	cfg, _, err := loader.Load(ctx, root)
	if err != nil {
		return config.Config{}, fmt.Errorf("loading config: %w", err)
	}

	return cfg, nil
}

func resolveRoot(ctx context.Context, dir string) (string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolving -dir: %w", err)
		}

		return abs, nil
	}

	if out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output(); err == nil {
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
	fmt.Fprint(w, `wavezd - the wavez daemon

Usage:
  wavezd [-dir <path>] [-socket <path>]

Flags:
  -dir <path>     project root (defaults to the enclosing repo, then cwd)
  -socket <path>  unix socket path (defaults to <root>/.wavez/d.sock)
  -v              print version information
`)
}
