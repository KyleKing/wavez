package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrAlreadyRunning is returned by Manager.Start when a server is already
// active. Only one model fits in 16 GB at a time: two servers on the same
// 6 GB model OOM'd Metal in the measurement behind DESIGN.md's Model
// routing section, so Manager refuses a second start outright rather than
// letting a caller discover the OOM.
var ErrAlreadyRunning = errors.New("runtime: a llama-server is already running")

// Manager starts and stops at most one llama-server at a time.
type Manager struct {
	active       *Server
	newProcess   ProcessFactory
	check        HealthChecker
	newTicker    NewTickerFunc
	pollInterval time.Duration
	mu           sync.Mutex
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithProcessFactory overrides how Manager starts the llama-server
// process, for tests that drive a fake process.
func WithProcessFactory(f ProcessFactory) ManagerOption {
	return func(m *Manager) { m.newProcess = f }
}

// WithHealthChecker overrides how Manager polls for readiness, for tests
// that drive a fake health endpoint.
func WithHealthChecker(f HealthChecker) ManagerOption {
	return func(m *Manager) { m.check = f }
}

// WithTickerFactory overrides how Manager paces readiness retries, for
// tests that drive the retry loop without sleeping.
func WithTickerFactory(f NewTickerFunc) ManagerOption {
	return func(m *Manager) { m.newTicker = f }
}

// WithPollInterval overrides the delay between readiness retries.
func WithPollInterval(d time.Duration) ManagerOption {
	return func(m *Manager) { m.pollInterval = d }
}

// NewManager builds a Manager that starts real llama-server processes and
// polls a real HTTP health endpoint, unless overridden by ManagerOption.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		newProcess:   newExecProcess,
		check:        HTTPHealthCheck,
		newTicker:    newRealTicker,
		pollInterval: DefaultPollInterval,
	}
	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Start starts one llama-server from cfg and waits for it to report ready.
// It returns ErrAlreadyRunning if a server started by this Manager is
// already active.
func (m *Manager) Start(ctx context.Context, cfg Config) (*Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != nil {
		return nil, ErrAlreadyRunning
	}

	srv, err := startServer(ctx, cfg, startDeps{
		newProcess:   m.newProcess,
		check:        m.check,
		newTicker:    m.newTicker,
		pollInterval: m.pollInterval,
	})
	if err != nil {
		return nil, err
	}

	m.active = srv

	return srv, nil
}

// Stop stops the active server, if any, and clears it so Start may run
// again.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return nil
	}

	err := m.active.Stop(ctx)
	m.active = nil

	return err
}

// Active returns the currently running Server, or nil when none is
// active.
func (m *Manager) Active() *Server {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.active
}
