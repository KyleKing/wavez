package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNoServer reports a file no configured server handles.
var ErrNoServer = errors.New("lsp: no server configured for this file type")

// ErrServerUnavailable reports a server whose binary is in neither the
// project's own tool directories nor on PATH. It is a property of the
// machine rather than of the project, which is why callers separate it from
// a server that starts and then fails.
var ErrServerUnavailable = errors.New("lsp: server binary not found in the project or on PATH")

// Server describes how to launch one language server and which files it
// handles.
type Server struct {
	Env      map[string]string
	Language string
	Command  string
	Args     []string
	// Manifests are the project-relative dependency files whose contents
	// decide what this server can resolve. A language server computes module
	// resolution once at startup and never revisits it, so a dependency
	// added mid-run reads as unresolvable until the server is restarted, and
	// the run is handed a type error it cannot fix by editing anything.
	Manifests  []string
	Extensions []string
}

// GoServer is gopls, which speaks LSP over stdio when invoked with no
// arguments.
func GoServer() Server {
	return Server{
		Language:   "go",
		Command:    "gopls",
		Manifests:  []string{"go.mod", "go.sum"},
		Extensions: []string{".go"},
	}
}

// PythonServer is ty, Astral's type checker, which speaks LSP over stdio
// under its `server` subcommand and publishes versioned diagnostics rather
// than only answering pull requests for them.
func PythonServer() Server {
	return Server{
		Language:   "python",
		Command:    "ty",
		Args:       []string{"server"},
		Manifests:  []string{"Pipfile.lock", "poetry.lock", "pyproject.toml", "requirements.txt", "uv.lock"},
		Extensions: []string{".py", ".pyi"},
	}
}

// Pool holds at most one server process per language for one project root,
// started the first time a file that server handles is requested. Callers
// share clients, so only the owner of the Pool may Close it.
type Pool struct {
	entries map[string]*entry
	root    string
	servers []Server
	mu      sync.Mutex
}

type entry struct {
	client *Client
	stamp  string
	mu     sync.Mutex
}

// NewPool builds a Pool over root. Passing no servers configures the default
// set, which is gopls and ty. A server whose binary is absent costs nothing
// until a file it claims is asked for, so the set is the languages wavez
// speaks rather than the ones this machine has installed.
func NewPool(root string, servers ...Server) *Pool {
	if len(servers) == 0 {
		servers = []Server{GoServer(), PythonServer()}
	}

	return &Pool{root: root, servers: servers, entries: make(map[string]*entry)}
}

// Handles reports whether any configured server claims this file.
func (p *Pool) Handles(path string) bool {
	_, ok := p.serverFor(path)

	return ok
}

// Client returns the running server for path's language, starting it on the
// first call. It wraps ErrNoServer when no server claims the file and
// ErrServerUnavailable when the server's binary is absent, and every caller
// asking for the same language gets the same client.
func (p *Pool) Client(ctx context.Context, path string) (*Client, error) {
	srv, ok := p.serverFor(path)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoServer, filepath.Ext(path))
	}

	bin, ok := p.lookup(srv.Command)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServerUnavailable, srv.Command)
	}

	srv.Command = bin

	p.mu.Lock()
	e, ok := p.entries[srv.Language]

	if !ok {
		e = &entry{}
		p.entries[srv.Language] = e
	}
	p.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	stamp := manifestStamp(p.root, srv.Manifests)

	if e.client != nil && e.stamp == stamp {
		return e.client, nil
	}

	if e.client != nil {
		//nolint:errcheck // a server being replaced has nothing left to report
		_ = e.client.Close(ctx)
		e.client = nil
	}

	client, err := newClient(ctx, p.root, srv)
	if err != nil {
		return nil, err
	}

	e.client, e.stamp = client, stamp

	return client, nil
}

// manifestStamp summarizes the dependency files a server resolves against,
// so a changed one restarts it. Size and modification time are enough: this
// answers whether the environment moved, not what it now holds, and reading
// every lockfile on each diagnostic would cost more than the restart it
// avoids. A missing file contributes its absence, which is what makes the
// first `uv add` in a project count as a change.
func manifestStamp(root string, manifests []string) string {
	var b strings.Builder

	for _, name := range manifests {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			b.WriteString(name + ":-\n")

			continue
		}

		fmt.Fprintf(&b, "%s:%d:%d\n", name, info.Size(), info.ModTime().UnixNano())
	}

	return b.String()
}

// projectBinDirs are the directories a project keeps its own tools in,
// relative to the root. A Python project installs `ty` into its virtualenv
// and a Node one installs its server into `node_modules`, so neither is on
// the PATH of a shell that never activated anything, and wavez reported a
// server it was standing next to as absent.
var projectBinDirs = []string{
	filepath.Join(".venv", "bin"),
	filepath.Join("node_modules", ".bin"),
	filepath.Join("venv", "bin"),
}

// lookup resolves a server command to an executable, preferring the
// project's own tool directories over the ambient PATH so the server that
// runs is the version the project pinned. An absolute command is taken as
// given.
func (p *Pool) lookup(command string) (string, bool) {
	if filepath.IsAbs(command) {
		_, err := os.Stat(command)

		return command, err == nil
	}

	for _, dir := range projectBinDirs {
		candidate := filepath.Join(p.root, dir, command)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, true
		}
	}

	path, err := exec.LookPath(command)
	if err != nil {
		return "", false
	}

	return path, true
}

// Close shuts every running server down.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	entries := make([]*entry, 0, len(p.entries))

	for _, e := range p.entries {
		entries = append(entries, e)
	}

	clear(p.entries)
	p.mu.Unlock()

	var errs []error

	for _, e := range entries {
		if e.client != nil {
			errs = append(errs, e.client.Close(ctx))
		}
	}

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("closing lsp pool: %w", err)
	}

	return nil
}

func (p *Pool) serverFor(path string) (Server, bool) {
	ext := strings.ToLower(filepath.Ext(path))

	for _, srv := range p.servers {
		for _, e := range srv.Extensions {
			if strings.EqualFold(e, ext) {
				return srv, true
			}
		}
	}

	return Server{}, false
}
