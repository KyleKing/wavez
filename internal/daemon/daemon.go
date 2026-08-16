// Package daemon implements wavezd's server side of the socket API: a
// unix-socket listener, a thread manager, and a Broker that turns a
// permission request or a question from any thread into an api.PendingInfo
// answerable from any connected client. Threads live as long as the Server
// does, so a client disconnecting and reconnecting resumes rather than
// losing work.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/kyleking/wavez/internal/agent"
)

const (
	defaultShutdownGrace = 10 * time.Second
	dialProbeTimeout     = 200 * time.Millisecond
)

// Sentinel errors New, Serve, and Shutdown return.
var (
	ErrLoopRequired           = errors.New("daemon: WithLoop is required")
	ErrBrokerRequired         = errors.New("daemon: WithBroker is required")
	ErrLogDirRequired         = errors.New("daemon: WithLogDir is required")
	ErrAlreadyServing         = errors.New("daemon: already serving")
	ErrNotServing             = errors.New("daemon: not serving")
	ErrDaemonAlreadyListening = errors.New("daemon: a daemon is already listening on this socket")
)

// StatsSource supplies the memory and model numbers Diagnostics cannot
// derive from what the daemon already tracks, so a test can report fixed
// values instead of reading the real machine.
type StatsSource interface {
	Stats() MemStats
}

// MemStats is one snapshot of machine and model memory.
type MemStats struct {
	UsedBytes  uint64
	TotalBytes uint64
	ModelBytes uint64
}

type config struct {
	loop          *agent.Loop
	broker        *Broker
	stats         StatsSource
	logDir        string
	prefix        agent.Prefix
	shutdownGrace time.Duration
}

// Option configures a Server at New.
type Option func(*config)

// WithLoop sets the agent.Loop every thread's turns run against. Required.
func WithLoop(loop *agent.Loop) Option {
	return func(c *config) { c.loop = loop }
}

// WithBroker sets the Broker threads ask permission and questions through.
// The same Broker must back the permission.Gate and question Asker wired
// into loop (via Broker.Gate and Broker.Asker), or an answer from a client
// never reaches the call waiting on it. Required.
func WithBroker(b *Broker) Option {
	return func(c *config) { c.broker = b }
}

// WithLogDir sets the directory each thread's event log is opened under.
// Required.
func WithLogDir(dir string) Option {
	return func(c *config) { c.logDir = dir }
}

// WithPrefix sets the system prompt and tool specs held stable across every
// turn of every thread.
func WithPrefix(prefix agent.Prefix) Option {
	return func(c *config) { c.prefix = prefix }
}

// WithStatsSource injects the memory and model numbers Diagnostics reports.
func WithStatsSource(s StatsSource) Option {
	return func(c *config) { c.stats = s }
}

// WithShutdownGrace bounds how long Shutdown waits for in-flight turns to
// finish on their own before canceling them. Defaults to 10s.
func WithShutdownGrace(d time.Duration) Option {
	return func(c *config) { c.shutdownGrace = d }
}

// Server is the daemon side of the socket API. It accepts connections on a
// unix socket, holds every live thread through an internal manager, and
// answers pending prompts from any connected client.
type Server struct {
	stats      StatsSource
	ln         net.Listener
	mgr        *manager
	broker     *Broker
	conns      map[*conn]struct{}
	acceptDone chan struct{}
	sockPath   string
	connsWG    sync.WaitGroup
	grace      time.Duration
	mu         sync.Mutex
	serving    bool
}

// New builds a Server bound to sockPath. It does not listen until Serve is
// called.
func New(sockPath string, opts ...Option) (*Server, error) {
	c := config{shutdownGrace: defaultShutdownGrace}
	for _, opt := range opts {
		opt(&c)
	}
	if c.loop == nil {
		return nil, ErrLoopRequired
	}
	if c.broker == nil {
		return nil, ErrBrokerRequired
	}
	if c.logDir == "" {
		return nil, ErrLogDirRequired
	}

	mgr := newManager(c.logDir, c.loop, c.prefix)
	s := &Server{
		mgr:      mgr,
		broker:   c.broker,
		stats:    c.stats,
		sockPath: sockPath,
		grace:    c.shutdownGrace,
		conns:    make(map[*conn]struct{}),
	}
	c.broker.attach(mgr, s.wakePending)

	return s, nil
}

// Serve accepts connections until ctx is done or Shutdown is called, and
// blocks until the daemon has fully stopped. It returns the error Shutdown
// hit tearing the daemon down, if any.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	if s.serving {
		s.mu.Unlock()

		return ErrAlreadyServing
	}
	s.serving = true
	s.mu.Unlock()

	ln, err := listen(ctx, s.sockPath)
	if err != nil {
		s.mu.Lock()
		s.serving = false
		s.mu.Unlock()

		return fmt.Errorf("listening on %s: %w", s.sockPath, err)
	}

	acceptDone := make(chan struct{})
	s.mu.Lock()
	s.ln = ln
	s.acceptDone = acceptDone
	s.mu.Unlock()

	acceptErr := make(chan error, 1)
	// A thread created or sent a prompt from here outlives its connection, so it runs against
	// m.ctx, not this ctx.
	//nolint:contextcheck // see comment above
	go func() {
		defer close(acceptDone)
		acceptErr <- s.acceptLoop()
	}()

	select {
	case <-ctx.Done():
		// ctx is already canceled; Shutdown's grace period needs a fresh context.
		//nolint:contextcheck // see comment above
		return s.Shutdown(context.Background())
	case err := <-acceptErr:
		return err
	}
}

func (s *Server) acceptLoop() error {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			serving := s.serving
			s.mu.Unlock()
			if !serving {
				return nil
			}

			return fmt.Errorf("accepting connection: %w", err)
		}

		s.connsWG.Add(1)
		go s.handleConn(nc)
	}
}

// Shutdown stops accepting connections, closes every client connection,
// waits up to its configured grace period for in-flight turns to finish on
// their own (canceling anything still running past the deadline), flushes
// and closes every thread's event log, and removes the socket file.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.serving {
		s.mu.Unlock()

		return ErrNotServing
	}
	s.serving = false
	ln := s.ln
	acceptDone := s.acceptDone
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if ln != nil {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("closing listener: %w", err)
		}
		<-acceptDone // the accept loop goroutine has fully returned, not just been told to
	}

	var closeErrs []error
	for _, c := range conns {
		if err := c.c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErrs = append(closeErrs, err)
		}
	}
	s.connsWG.Wait()
	if err := errors.Join(closeErrs...); err != nil {
		return fmt.Errorf("closing connections: %w", err)
	}

	graceCtx, cancel := context.WithTimeout(ctx, s.grace)
	defer cancel()
	s.mgr.waitIdle(graceCtx)
	s.mgr.closeAll()

	if err := os.Remove(s.sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing socket: %w", err)
	}

	return nil
}

func (s *Server) wakePending() {
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// listen opens the unix socket at path, detecting and replacing a stale
// socket file left by a daemon that crashed rather than a live one still
// listening.
func listen(ctx context.Context, path string) (net.Listener, error) {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "unix", path)
	if err == nil {
		return ln, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}

	probeCtx, cancel := context.WithTimeout(ctx, dialProbeTimeout)
	defer cancel()

	var d net.Dialer
	if nc, dialErr := d.DialContext(probeCtx, "unix", path); dialErr == nil {
		if closeErr := nc.Close(); closeErr != nil {
			return nil, fmt.Errorf("closing probe connection: %w", closeErr)
		}

		return nil, ErrDaemonAlreadyListening
	}

	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return nil, fmt.Errorf("removing stale socket %s: %w", path, rmErr)
	}

	ln, err = lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on %s after removing stale socket: %w", path, err)
	}

	return ln, nil
}
