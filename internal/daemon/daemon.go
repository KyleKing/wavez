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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/sched"
	"github.com/kyleking/wavez/internal/thread"
)

const (
	defaultShutdownGrace = 10 * time.Second
	dialProbeTimeout     = 200 * time.Millisecond
	// The sun_path limit: macOS allows 104 bytes including the terminator.
	maxSockPath = 103
)

// Sentinel errors New, Serve, and Shutdown return.
var (
	ErrLoopRequired   = errors.New("daemon: WithLoop is required")
	ErrBrokerRequired = errors.New("daemon: WithBroker is required")
	ErrLogDirRequired = errors.New("daemon: WithLogDir is required")
	// ErrSockPathTooLong reports a socket path past the platform's sun_path
	// limit, which otherwise surfaces as a bare "invalid argument".
	ErrSockPathTooLong        = errors.New("daemon: socket path too long")
	ErrAlreadyServing         = errors.New("daemon: already serving")
	ErrNotServing             = errors.New("daemon: not serving")
	ErrDaemonAlreadyListening = errors.New("daemon: a daemon is already listening on this socket")
	// ErrRootRequired reports a "new" (or a routines request with no
	// default project) that named no root: a daemon serving several
	// projects has no scope to fall back to, unlike the single-project
	// sugar WithLoop builds.
	ErrRootRequired = errors.New("daemon: root is required")
	// ErrProjectNotLoaded reports a root a Server has neither loaded nor
	// can load, because it was built without a Loader.
	ErrProjectNotLoaded = errors.New("daemon: project not loaded")
)

// StatsSource supplies the memory and model numbers Diagnostics cannot
// derive from what the daemon already tracks, so a test can report fixed
// values instead of reading the real machine.
type StatsSource interface {
	Stats() MachineStats
}

// MachineStats is one snapshot of machine memory and CPU. A source that
// cannot take a reading leaves the matching Measured flag false, so the panel
// shows that row as unavailable instead of as zero.
type MachineStats struct {
	UsedBytes  uint64
	TotalBytes uint64
	// ModelBytes is the local model server's resident set.
	ModelBytes uint64
	// CPUPercent is the whole machine as a share of one core summed across
	// processes, which is how ps reports it and can exceed 100.
	CPUPercent float64
	CPUDaemon  float64
	CPUModel   float64
	// ModelMeasured reports whether ModelBytes is a reading.
	ModelMeasured bool
	// CPUMeasured reports whether the three CPU numbers are readings.
	CPUMeasured bool
}

// CycleSource resolves the phased ways of working a thread may run and
// builds the Driver each phase's model work goes through. *app.App
// satisfies it.
type CycleSource interface {
	Cycle(name string) (cycle.Cycle, error)
	CycleDriver(base thread.ID, dirs []string, hint router.Input) cycle.Driver
}

type config struct {
	loop              *agent.Loop
	cycles            CycleSource
	broker            *Broker
	leases            *lease.Manager
	scheduler         *sched.Scheduler
	stats             StatsSource
	differ            Differ
	restorer          Restorer
	expander          Expander
	routines          RoutineSource
	models            ModelStore
	loader            Loader
	logDir            string
	root              string
	modelSettingsPath string
	prefix            agent.Prefix
	shutdownGrace     time.Duration
}

// Option configures a Server at New.
type Option func(*config)

// WithLoop sets the agent.Loop every thread's turns run against, and is the
// single-project sugar over WithLoader: a Server built with it loads one
// project (root from WithRoot, or unnamed) at New rather than lazily. A
// Server serving several projects behind one socket uses WithLoader instead
// and builds each Project as a request first names its root.
func WithLoop(loop *agent.Loop) Option {
	return func(c *config) { c.loop = loop }
}

// Loader builds a Project the first time a request names its root. A
// Server caches what it returns for the root's lifetime: this lane never
// unloads a project. The daemon binary wires this to app.New, and a test
// wires it to a fixture that needs neither a model nor a pkl evaluator.
type Loader func(ctx context.Context, root string) (*Project, error)

// WithLoader sets the function that loads a project the first time a
// request names its root, letting one Server serve several project roots
// behind one socket. A CmdNew or CmdList naming no root falls back to the
// project WithLoop built, if any; otherwise ErrRootRequired.
func WithLoader(l Loader) Option {
	return func(c *config) { c.loader = l }
}

// WithModelSettingsPath sets where per-model runtime settings persist. It
// answers for the whole laptop, not one project, so a Server serving
// several projects sets this once instead of taking it from any one
// project's directory. Unset, settings are held in memory only.
func WithModelSettingsPath(path string) Option {
	return func(c *config) { c.modelSettingsPath = path }
}

// WithCycles lets a thread run a named Cycle instead of a single loop. A
// Server without one refuses a cycle request rather than silently running
// the prompt as an ordinary turn, since the phases are the work.
func WithCycles(c CycleSource) Option {
	return func(cfg *config) { cfg.cycles = c }
}

// WithBroker sets the Broker threads ask permission and questions through.
// The same Broker must back the permission.Gate and question Asker wired
// into loop (via Broker.Gate and Broker.Asker), or an answer from a client
// never reaches the call waiting on it. Required.
func WithBroker(b *Broker) Option {
	return func(c *config) { c.broker = b }
}

// WithLogDir sets the directory each thread's event log is opened under,
// for the project WithLoop builds. Required alongside WithLoop.
func WithLogDir(dir string) Option {
	return func(c *config) { c.logDir = dir }
}

// WithPrefix sets the system prompt and tool specs held stable across every
// turn of every thread.
func WithPrefix(prefix agent.Prefix) Option {
	return func(c *config) { c.prefix = prefix }
}

// WithRoot sets the project a thread created without an explicit directory
// set belongs to. The protocol documents Dirs as defaulting to the daemon's
// scope, and without this a thread created from the new-thread form has no
// directory at all.
func WithRoot(root string) Option {
	return func(c *config) { c.root = root }
}

// WithDiffer sets the source of a thread's unified diff. A Server without
// one answers a diff request with an empty diff rather than an error, since
// a project outside a repository legitimately has none.
func WithDiffer(d Differ) Option {
	return func(c *config) { c.differ = d }
}

// WithRestorer sets the backend an undo runs through. A Server without one
// refuses a restore rather than reporting an undo it never performed.
func WithRestorer(r Restorer) Option {
	return func(c *config) { c.restorer = r }
}

// WithExpander sets the @file and @symbol resolver applied to every prompt
// before it reaches the model. A Server without one passes prompts through
// unchanged.
func WithExpander(e Expander) Option {
	return func(c *config) { c.expander = e }
}

// WithLeases sets the lease manager the write tools acquire through, so the
// daemon can report who holds what and say which thread is waiting on whom.
// It must be the same manager the tool registry was built with.
func WithLeases(m *lease.Manager) Option {
	return func(c *config) { c.leases = m }
}

// WithScheduler sets the memory-aware admission every local turn passes
// through. It must be the same scheduler the gate runner was built with, or
// a turn and a gate run will not see each other.
func WithScheduler(s *sched.Scheduler) Option {
	return func(c *config) { c.scheduler = s }
}

// WithModelStore sets the backend the model commands run through. A Server
// without one refuses every model command rather than reporting an empty
// list, since "no models installed" and "no way to ask" are different answers.
func WithModelStore(m ModelStore) Option {
	return func(c *config) { c.models = m }
}

// WithStatsSource injects the memory and CPU numbers Diagnostics reports.
func WithStatsSource(s StatsSource) Option {
	return func(c *config) { c.stats = s }
}

// WithShutdownGrace bounds how long Shutdown waits for in-flight turns to
// finish on their own before canceling them. Defaults to 10s.
func WithShutdownGrace(d time.Duration) Option {
	return func(c *config) { c.shutdownGrace = d }
}

// Server is the daemon side of the socket API. It accepts connections on a
// unix socket, holds every loaded project's threads, and answers pending
// prompts from any connected client. Its sched field is the one
// memory-aware scheduler shared by every project, since admission answers
// for the whole laptop rather than for one project.
type Server struct {
	//nolint:containedctx // scopes connection handling, independent of any one project's lifetime
	ctx            context.Context
	cancelAll      context.CancelFunc
	stats          StatsSource
	sched          *sched.Scheduler
	modelStore     ModelStore
	modelReg       *modelRegistry
	window         *sampleWindow
	ln             net.Listener
	broker         *Broker
	loader         Loader
	defaultProject *Project
	projects       map[string]*Project
	threadIndex    map[string]*Project
	conns          map[*conn]struct{}
	acceptDone     chan struct{}
	sockPath       string
	connsWG        sync.WaitGroup
	grace          time.Duration
	mu             sync.Mutex
	serving        bool
}

// New builds a Server bound to sockPath. It does not listen until Serve is
// called.
func New(sockPath string, opts ...Option) (*Server, error) {
	c := config{shutdownGrace: defaultShutdownGrace}
	for _, opt := range opts {
		opt(&c)
	}
	if c.broker == nil {
		return nil, ErrBrokerRequired
	}

	settingsPath := c.modelSettingsPath
	if settingsPath == "" && c.logDir != "" {
		settingsPath = modelSettingsPath(c.logDir)
	}

	ctx, cancelAll := context.WithCancel(context.Background())
	s := &Server{
		ctx:         ctx,
		cancelAll:   cancelAll,
		broker:      c.broker,
		stats:       c.stats,
		modelStore:  c.models,
		modelReg:    newModelRegistry(settingsPath),
		window:      newSampleWindow(time.Now),
		sockPath:    sockPath,
		grace:       c.shutdownGrace,
		conns:       make(map[*conn]struct{}),
		sched:       c.scheduler,
		loader:      c.loader,
		projects:    make(map[string]*Project),
		threadIndex: make(map[string]*Project),
	}
	c.broker.attach(s, s.wakePending)

	if c.loop != nil {
		if err := s.buildDefaultProject(c); err != nil {
			return nil, err
		}
	}

	if c.scheduler != nil {
		c.scheduler.OnHold(s.noteHold)
	}

	return s, nil
}

// buildDefaultProject builds and registers the single-project sugar
// WithLoop configures, and is only called once c.loop is known non-nil.
func (s *Server) buildDefaultProject(c config) error {
	p, err := NewProject(c.root, ProjectConfig{
		Loop: c.loop, Cycles: c.cycles, Expander: c.expander, Scheduler: c.scheduler,
		Leases: c.leases, Differ: c.differ, Restorer: c.restorer, Routines: c.routines,
		Prefix: c.prefix, LogDir: c.logDir,
	})
	if err != nil {
		return err
	}
	s.defaultProject = p

	if c.root != "" {
		key, err := canonicalRoot(c.root)
		if err != nil {
			return err
		}
		s.projects[key] = p
	}

	if c.leases != nil {
		c.leases.OnWait(s.noteLeaseWait)
	}

	return nil
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

	// The sampler outlives any one request and is stopped by this defer, so
	// it takes its own context rather than inheriting a caller's.
	sampleCtx, stopSampling := context.WithCancel(context.Background())
	defer stopSampling()

	//nolint:contextcheck // see the comment above
	go s.sample(sampleCtx)

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
	s.cancelAll()

	if err := s.closeProjects(graceCtx); err != nil {
		return err
	}

	if err := os.Remove(s.sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing socket: %w", err)
	}

	return nil
}

// closeProjects waits for every loaded project's in-flight turns to finish
// (or graceCtx to expire, whichever comes first), then closes each
// project's threads and, for a project a Loader built, releases whatever
// it built alongside them.
func (s *Server) closeProjects(graceCtx context.Context) error {
	projects := s.projectsSnapshot()
	for _, p := range projects {
		p.mgr.waitIdle(graceCtx)
	}

	var projectErrs []error
	for _, p := range projects {
		p.mgr.closeAll()
		if p.closer == nil {
			continue
		}
		if err := p.closer(); err != nil {
			projectErrs = append(projectErrs, err)
		}
	}
	if err := errors.Join(projectErrs...); err != nil {
		return fmt.Errorf("closing projects: %w", err)
	}

	return nil
}

// modelSettingsPath keeps per-model runtime settings beside the thread logs,
// so an edit survives a restart without a config-file round trip.
func modelSettingsPath(logDir string) string {
	if logDir == "" {
		return ""
	}

	return filepath.Join(filepath.Dir(logDir), "models.json")
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
	if len(path) > maxSockPath {
		return nil, fmt.Errorf("%w: %d bytes, limit is %d: %s", ErrSockPathTooLong, len(path), maxSockPath, path)
	}

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
