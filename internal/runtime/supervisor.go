package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/sysinfo"
)

// DefaultPort is the loopback port llama-server listens on unless Config
// overrides it.
const DefaultPort = 8080

// DefaultStartTimeout bounds one Start attempt. Cold start measured 1.6 s
// to first token on this laptop; the rest of the budget covers reading a
// 5 GB blob past a cold page cache.
const DefaultStartTimeout = 60 * time.Second

// LocalBaseURL is the OpenAI-compatible endpoint root of a llama-server on
// port, so a caller that never reached Ensure still addresses the same
// endpoint.
func LocalBaseURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/v1", port)
}

func localHealthURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// Endpoint is a llama-server that answered a health check.
type Endpoint struct {
	// BaseURL is what an openaic.Client should be pointed at.
	BaseURL string
	// Managed reports whether this process started the server. A false
	// value means the user was already serving a model and Stop leaves it
	// alone.
	Managed bool
}

// Supervisor hands out one healthy local endpoint: an already-running
// llama-server when the configured port answers, otherwise one it starts
// from the model's GGUF and owns until Stop. Only one model fits in 16 GB,
// so it never starts a second server beside a live one.
type Supervisor struct {
	manager      *Manager
	resolve      GGUFResolver
	probe        HealthChecker
	model        string
	cfg          Config
	startTimeout time.Duration
	slots        int
	mu           sync.Mutex
}

// SupervisorOption configures a Supervisor.
type SupervisorOption func(*Supervisor)

// WithGGUFResolver overrides how a model name becomes a GGUF path.
func WithGGUFResolver(r GGUFResolver) SupervisorOption {
	return func(s *Supervisor) { s.resolve = r }
}

// WithProbe overrides the health check that decides whether a server is
// already running on the configured port.
func WithProbe(c HealthChecker) SupervisorOption {
	return func(s *Supervisor) { s.probe = c }
}

// WithAdmittedSlots tells the Supervisor how many slots the scheduler
// admits, which the cache is split across.
func WithAdmittedSlots(n int) SupervisorOption {
	return func(s *Supervisor) { s.slots = n }
}

// WithStartTimeout bounds how long Ensure waits for a started server to
// report ready.
func WithStartTimeout(d time.Duration) SupervisorOption {
	return func(s *Supervisor) { s.startTimeout = d }
}

// WithManager overrides the Manager a Supervisor starts servers through,
// for tests that inject a fake process, health check, and ticker.
func WithManager(m *Manager) SupervisorOption {
	return func(s *Supervisor) { s.manager = m }
}

// NewSupervisor builds a Supervisor for one Ollama model name (e.g.
// "qwen3:8b"). A non-empty cfg.GGUFPath skips resolution; cfg.Port defaults
// to DefaultPort.
func NewSupervisor(model string, cfg Config, opts ...SupervisorOption) *Supervisor {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}

	s := &Supervisor{
		manager:      NewManager(),
		resolve:      ResolveGGUF,
		probe:        HTTPHealthCheck,
		slots:        ServedSlots,
		model:        model,
		cfg:          cfg,
		startTimeout: DefaultStartTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Ensure returns an endpoint serving the model, starting llama-server only
// when the configured port has none. Every failure names its cause: an
// unresolvable model, a binary that would not start, or a server that never
// reported ready.
func (s *Supervisor) Ensure(ctx context.Context) (Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if srv := s.manager.Active(); srv != nil {
		return Endpoint{BaseURL: srv.BaseURL(), Managed: true}, nil
	}

	if err := s.probe(ctx, localHealthURL(s.cfg.Port)); err == nil {
		return Endpoint{BaseURL: LocalBaseURL(s.cfg.Port)}, nil
	}

	cfg := s.cfg

	if cfg.GGUFPath == "" {
		path, err := s.resolve(ctx, s.model)
		if err != nil {
			return Endpoint{}, fmt.Errorf("resolving GGUF for %s: %w", s.model, err)
		}

		cfg.GGUFPath = path
	}

	mem, memRead := readMemoryOnce(ctx, sysinfo.ReadMemory)

	cacheMiB, cacheSource := DeriveCacheRAMMiB(CacheRAMInput{
		OverrideMiB: s.cfg.CacheRAMMiB,
		Mem:         mem,
		MemRead:     memRead,
		ModelBytes:  modelRAMBytes(cfg.GGUFPath),
		Slots:       s.slots,
	})
	cfg.CacheRAMMiB = cacheMiB

	slog.Info("sizing the llama-server prompt cache", "cacheRAMMiB", cfg.CacheRAMMiB, "from", cacheSource)

	startCtx, cancel := context.WithTimeout(ctx, s.startTimeout)
	defer cancel()

	srv, err := s.manager.Start(startCtx, cfg)
	if err != nil {
		return Endpoint{}, fmt.Errorf("starting llama-server for %s: %w", s.model, err)
	}

	return Endpoint{BaseURL: srv.BaseURL(), Managed: true}, nil
}

// Stop stops the server this Supervisor started, and does nothing when
// Ensure reused one it does not own. A leaked server holds 6 GB, so ctx
// must carry a deadline.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.manager.Stop(ctx)
}
