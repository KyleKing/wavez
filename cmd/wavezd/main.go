// Package main is the wavez daemon. One process serves every project on
// this laptop over one unix socket: it holds each project's threads, agent
// loop, and stores, loading a project the first time a client names its
// root.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/ollama"
	"github.com/kyleking/wavez/internal/sched"
	"github.com/kyleking/wavez/internal/sysinfo"
	"github.com/kyleking/wavez/internal/vcs"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	slog.SetDefault(slog.New(lsp.Quiet(slog.NewTextHandler(os.Stderr, nil))))

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
	fs.StringVar(&dir, "dir", "", "a project root to load at startup (none preloads nothing)")
	fs.StringVar(&sock, "socket", "", "unix socket path (defaults to the per-laptop user config dir)")
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

// serve starts one daemon for the whole laptop. It loads no project at all
// until a client names a root, except dir, which is preloaded so the first
// thread in that project does not pay to load it.
func serve(ctx context.Context, dir, sock string) error {
	if sock == "" {
		var err error

		sock, err = config.UserSocketPath()
		if err != nil {
			return fmt.Errorf("resolving default socket path: %w", err)
		}
	}

	userDir, err := config.UserDir()
	if err != nil {
		return fmt.Errorf("resolving user config dir: %w", err)
	}

	broker := daemon.NewBroker()
	// Memory admission answers for the whole laptop: one scheduler, shared
	// by every project this daemon loads, built once against the fixed
	// default headroom rather than any one project's ".wavez.pkl", which a
	// per-project Scheduler could otherwise read.
	scheduler := sched.New(sched.WithHeadroom(config.DefaultAdmissionHeadroom))
	settingsPath := filepath.Join(userDir, "models.json")

	srv, err := daemon.New(sock,
		daemon.WithBroker(broker),
		daemon.WithLoader(projectLoader(broker, scheduler, settingsPath)),
		daemon.WithStatsSource(&machineStats{ctx: ctx}),
		daemon.WithModelStore(ollama.New()),
		daemon.WithScheduler(scheduler),
		daemon.WithModelSettingsPath(settingsPath),
	)
	if err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	if dir != "" {
		abs, aerr := filepath.Abs(dir)
		if aerr != nil {
			return fmt.Errorf("resolving -dir: %w", aerr)
		}
		if perr := srv.Preload(ctx, abs); perr != nil {
			return fmt.Errorf("preloading %s: %w", abs, perr)
		}
	}

	fmt.Fprintf(os.Stderr, "wavezd listening on %s\n", sock)

	if err := srv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serving: %w", err)
	}

	return nil
}

// projectLoader builds the daemon.Loader that turns a root a client named
// into a daemon.Project: one project's full object graph via app.New,
// sharing broker and scheduler with every other project this daemon loads,
// and serving the local model with whatever the models screen saved for it
// at settingsPath.
func projectLoader(broker *daemon.Broker, scheduler *sched.Scheduler, settingsPath string) daemon.Loader {
	return func(ctx context.Context, root string) (*daemon.Project, error) {
		cfg, err := loadConfig(ctx, root)
		if err != nil {
			return nil, err
		}

		a, err := app.New(ctx, root, cfg, broker.Gate(),
			app.WithAsker(broker.Asker()), app.WithManagedLocalServer(), app.WithScheduler(scheduler),
			app.WithLocalRuntime(daemon.SavedLocalRuntime(settingsPath, cfg.Tiers.Fast.Model)))
		if err != nil {
			return nil, fmt.Errorf("building project %s: %w", root, err)
		}

		p, err := daemon.NewProject(root, daemon.ProjectConfig{
			Loop:      a.Loop,
			Cycles:    a,
			Expander:  a.Mentions,
			Scheduler: scheduler,
			Leases:    a.Leases,
			Differ:    vcs.NewJj(),
			Restorer:  vcs.NewJj(),
			Routines:  a.Routines,
			Prefix:    prefix(a),
			LogDir:    filepath.Join(root, ".wavez", "threads"),
			Closer:    a.Close,
		})
		if err != nil {
			//nolint:contextcheck // shutdown must outlive the load's context the same way serve's does
			if cerr := a.Close(); cerr != nil {
				fmt.Fprintf(os.Stderr, "wavezd: closing %s after a failed load: %v\n", root, cerr)
			}

			return nil, fmt.Errorf("assembling daemon project %s: %w", root, err)
		}

		return p, nil
	}
}

// llamaServerCommand is the process the local model's footprint and CPU are
// read from, since llama-server is what wavez serves through.
const llamaServerCommand = "llama-server"

// statsTTL is how long one machine reading answers for. Reading the model's
// footprint costs a `top` sample of about half a second, and the daemon's
// sampler and every polling client would otherwise each pay it.
const statsTTL = time.Second

// machineStats reads real memory and CPU for the diagnostics panel. A reading
// that fails is reported as unmeasured rather than guessed, so the panel never
// invents a number.
type machineStats struct {
	ctx  context.Context //nolint:containedctx // StatsSource.Stats takes no context
	at   time.Time
	last daemon.MachineStats
	mu   sync.Mutex
}

func (m *machineStats) Stats() daemon.MachineStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	if time.Since(m.at) < statsTTL {
		return m.last
	}

	m.last, m.at = m.read(), time.Now()

	return m.last
}

func (m *machineStats) read() daemon.MachineStats {
	out := daemon.MachineStats{}

	if mem, err := sysinfo.ReadMemory(m.ctx); err == nil {
		out.UsedBytes, out.TotalBytes = mem.UsedBytes, mem.TotalBytes
	}

	procs, err := sysinfo.ReadProcesses(m.ctx)
	if err != nil {
		return out
	}

	out.CPUMeasured = true
	self := os.Getpid()

	for _, p := range procs {
		out.CPUPercent += p.CPUPercent

		switch {
		case p.PID == self:
			out.CPUDaemon += p.CPUPercent
		case p.Command == llamaServerCommand:
			out.CPUModel += p.CPUPercent

			if fp, err := sysinfo.ReadFootprint(m.ctx, p.PID); err == nil {
				out.ModelBytes += fp
				out.ModelMeasured = true
			}
		}
	}

	return out
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

func printHelp(w io.Writer) {
	//nolint:errcheck // best-effort usage output
	fmt.Fprint(w, `wavezd - the wavez daemon

One daemon serves every project on this laptop over one socket, loading a
project the first time a client names its root.

Usage:
  wavezd [-dir <path>] [-socket <path>]

Flags:
  -dir <path>     a project root to load at startup (none preloads nothing)
  -socket <path>  unix socket path (defaults to the per-laptop user config dir)
  -v              print version information
`)
}
