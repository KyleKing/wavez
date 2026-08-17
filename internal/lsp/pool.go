package lsp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNoServer reports a file no configured server handles.
var ErrNoServer = errors.New("lsp: no server configured for this file type")

// ErrServerUnavailable reports a server whose binary is not on PATH. It is a
// property of the machine rather than of the project, which is why callers
// separate it from a server that starts and then fails.
var ErrServerUnavailable = errors.New("lsp: server binary not found on PATH")

// Server describes how to launch one language server and which files it
// handles.
type Server struct {
	Env        map[string]string
	Language   string
	Command    string
	Args       []string
	Extensions []string
}

// GoServer is gopls, which speaks LSP over stdio when invoked with no
// arguments.
func GoServer() Server {
	return Server{Language: "go", Command: "gopls", Extensions: []string{".go"}}
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
	mu     sync.Mutex
}

// NewPool builds a Pool over root. Passing no servers configures the default
// set, which is gopls today.
func NewPool(root string, servers ...Server) *Pool {
	if len(servers) == 0 {
		servers = []Server{GoServer()}
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

	if _, err := exec.LookPath(srv.Command); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrServerUnavailable, srv.Command)
	}

	p.mu.Lock()
	e, ok := p.entries[srv.Language]

	if !ok {
		e = &entry{}
		p.entries[srv.Language] = e
	}
	p.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.client != nil {
		return e.client, nil
	}

	client, err := newClient(ctx, p.root, srv)
	if err != nil {
		return nil, err
	}

	e.client = client

	return client, nil
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
