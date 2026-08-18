package daemon

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/sched"
)

// Project bundles one project's daemon-side collaborators: its thread
// manager, its diff and restore backends, its routines, and the lease
// manager scoped to its own directory tree. A Server holds one Project per
// loaded root, built directly by NewProject (the single-project sugar) or
// lazily by a Loader the first time a request names that root, and never
// unloads one once loaded.
type Project struct {
	mgr      *manager
	leases   *lease.Manager
	differ   Differ
	restorer Restorer
	routines RoutineSource
	closer   func() error
	root     string
}

// ProjectConfig is what NewProject needs to build one project. Loop and
// LogDir are required; the rest are optional.
type ProjectConfig struct {
	Cycles    CycleSource
	Expander  Expander
	Differ    Differ
	Restorer  Restorer
	Routines  RoutineSource
	Loop      *agent.Loop
	Scheduler *sched.Scheduler
	Leases    *lease.Manager
	Closer    func() error
	LogDir    string
	Prefix    agent.Prefix
}

// NewProject builds root's Project. A Loader passed to WithLoader receives
// an already-canonicalized root from Server and should pass it straight
// through.
func NewProject(root string, cfg ProjectConfig) (*Project, error) {
	if cfg.Loop == nil {
		return nil, ErrLoopRequired
	}
	if cfg.LogDir == "" {
		return nil, ErrLogDirRequired
	}

	mgr := newManager(cfg.LogDir, cfg.Loop, cfg.Prefix)
	mgr.mentions = cfg.Expander
	mgr.cycles = cfg.Cycles
	mgr.defaultDirs = defaultDirs(root)
	mgr.scheduler = cfg.Scheduler

	return &Project{
		root:     root,
		mgr:      mgr,
		leases:   cfg.Leases,
		differ:   cfg.Differ,
		restorer: cfg.Restorer,
		routines: cfg.Routines,
		closer:   cfg.Closer,
	}, nil
}

// canonicalRoot is the key a root is cached and looked up under: an
// absolute, cleaned path, so "." and the enclosing repo's absolute path
// name the same project.
func canonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving root %s: %w", root, err)
	}

	return filepath.Clean(abs), nil
}

// resolveProject answers root's Project: the cache, then the Loader, then
// ErrProjectNotLoaded. An empty root falls back to the Server's default
// project (WithLoop's sugar), or ErrRootRequired when there is none.
func (s *Server) resolveProject(ctx context.Context, root string) (*Project, error) {
	if root == "" {
		s.mu.Lock()
		dp := s.defaultProject
		s.mu.Unlock()

		if dp == nil {
			return nil, ErrRootRequired
		}

		return dp, nil
	}

	key, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if p, ok := s.projects[key]; ok {
		s.mu.Unlock()

		return p, nil
	}
	loader := s.loader
	s.mu.Unlock()

	if loader == nil {
		return nil, fmt.Errorf("%w: %s", ErrProjectNotLoaded, key)
	}

	p, err := loader(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("loading project %s: %w", key, err)
	}

	return s.cacheProject(key, p), nil
}

// Preload loads root through the Server's Loader (or registers WithLoop's
// project under it, if root matches) ahead of any client naming it, so
// `wavezd -dir` can warm one project at startup instead of paying the
// first thread's create latency for it.
func (s *Server) Preload(ctx context.Context, root string) error {
	_, err := s.resolveProject(ctx, root)

	return err
}

// lookupProject answers root's Project without loading anything: the
// cache and the default project, nothing else. CmdList's Root filter uses
// this, since listing a project's threads should never have the side
// effect of loading it.
func (s *Server) lookupProject(root string) (*Project, bool) {
	if root == "" {
		s.mu.Lock()
		defer s.mu.Unlock()

		return s.defaultProject, s.defaultProject != nil
	}

	key, err := canonicalRoot(root)
	if err != nil {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[key]

	return p, ok
}

// cacheProject stores p under key unless a concurrent load already won,
// in which case the winner's Project is returned so both callers agree on
// one instance.
func (s *Server) cacheProject(key string, p *Project) *Project {
	s.mu.Lock()
	if existing, ok := s.projects[key]; ok {
		s.mu.Unlock()

		return existing
	}
	s.projects[key] = p
	s.mu.Unlock()

	if p.leases != nil {
		p.leases.OnWait(s.noteLeaseWait)
	}

	return p
}

// projectsSnapshot returns every distinct loaded Project, the default
// project included exactly once even when it is also cached under its own
// root.
func (s *Server) projectsSnapshot() []*Project {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[*Project]bool, len(s.projects)+1)
	out := make([]*Project, 0, len(s.projects)+1)

	if s.defaultProject != nil {
		seen[s.defaultProject] = true
		out = append(out, s.defaultProject)
	}
	for _, p := range s.projects {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	return out
}

// registerThread indexes id under p, so a later thread command that names
// only the id (every one but new) can find its project directly.
func (s *Server) registerThread(id string, p *Project) {
	s.mu.Lock()
	s.threadIndex[id] = p
	s.mu.Unlock()
}

// findByThread answers the Project id belongs to. Thread ids are random
// and drawn from a wide enough space to be unique across every project a
// Server loads, so no root qualifier is needed to look one up.
func (s *Server) findByThread(id string) (*Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.threadIndex[id]

	return p, ok
}

// get and appendState satisfy Broker's threadLookup by routing to id's
// project, so the Broker (which knows only thread ids) needs no notion of
// which project a pending prompt belongs to.
func (s *Server) get(id string) (*managedThread, bool) {
	p, ok := s.findByThread(id)
	if !ok {
		return nil, false
	}

	return p.mgr.get(id)
}

func (s *Server) appendState(id string, state event.State) error {
	p, ok := s.findByThread(id)
	if !ok {
		return ErrThreadNotFound
	}

	return p.mgr.appendState(id, state)
}

// park and unpark satisfy Broker's threadLookup the same way get and
// appendState do, routing to id's project.
func (s *Server) park(id string) error {
	p, ok := s.findByThread(id)
	if !ok {
		return ErrThreadNotFound
	}

	return p.mgr.park(id)
}

func (s *Server) unpark(ctx context.Context, id string) error {
	p, ok := s.findByThread(id)
	if !ok {
		return ErrThreadNotFound
	}

	return p.mgr.unpark(ctx, id)
}
