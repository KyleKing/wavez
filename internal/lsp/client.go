package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// ErrServerExited reports a server process that is no longer answering, which
// a caller must distinguish from a server that simply had nothing to say.
var ErrServerExited = errors.New("lsp: server exited")

// Client is one language server process for one project root. Every method is
// safe for concurrent use; documents stay open for the process's lifetime, so
// a second sync of the same file is a change rather than a re-open.
type Client struct {
	inner     *powernap.Client
	updates   chan struct{}
	versions  map[string]int
	published map[string]publication
	root      string
	server    Server
	mu        sync.Mutex
}

func newClient(ctx context.Context, root string, srv Server) (*Client, error) {
	rootURI := string(protocol.URIFromPath(root))

	inner, err := powernap.NewClient(powernap.ClientConfig{
		Command:          srv.Command,
		Args:             srv.Args,
		Environment:      srv.Env,
		RootURI:          rootURI,
		WorkspaceFolders: []protocol.WorkspaceFolder{{URI: rootURI, Name: filepath.Base(root)}},
		Settings:         map[string]any{},
	})
	if err != nil {
		return nil, fmt.Errorf("starting %s: %w", srv.Command, err)
	}

	c := &Client{
		inner:     inner,
		updates:   make(chan struct{}),
		versions:  make(map[string]int),
		published: make(map[string]publication),
		server:    srv,
		root:      root,
	}

	inner.RegisterNotificationHandler("textDocument/publishDiagnostics", c.onPublish)

	if err := inner.Initialize(ctx, false); err != nil {
		inner.Kill()

		return nil, fmt.Errorf("initializing %s: %w", srv.Command, err)
	}

	return c, nil
}

// Language is the LSP language identifier this client's server was launched
// for.
func (c *Client) Language() string { return c.server.Language }

// Sync sends the file's current contents to the server, as a didOpen the first
// time and a didChange after, and returns the document version the server
// echoes on diagnostics for it.
func (c *Client) Sync(ctx context.Context, path string) (int, error) {
	abs, err := c.abs(path)
	if err != nil {
		return 0, err
	}

	src, err := os.ReadFile(abs) //nolint:gosec // paths come from the caller's own change set
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	uri := string(protocol.URIFromPath(abs))

	c.mu.Lock()
	version, opened := c.versions[abs]
	version++
	c.versions[abs] = version
	c.mu.Unlock()

	if !opened {
		if err := c.inner.NotifyDidOpenTextDocument(ctx, uri, c.server.Language, version, string(src)); err != nil {
			return 0, fmt.Errorf("didOpen %s: %w", path, err)
		}

		return version, nil
	}

	whole := protocol.TextDocumentContentChangeWholeDocument{Text: string(src)}

	change := []protocol.TextDocumentContentChangeEvent{{Value: whole}}
	if err := c.inner.NotifyDidChangeTextDocument(ctx, uri, version, change); err != nil {
		return 0, fmt.Errorf("didChange %s: %w", path, err)
	}

	return version, nil
}

// Diagnostics blocks until the server has published diagnostics for path at
// version or later, and returns them. It returns ctx's error when the wait
// runs out, so a caller's deadline is what bounds a server that never answers.
//
// A publication carrying no version satisfies any wait: the protocol makes the
// field optional, and a server that omits it has no way to say "later".
func (c *Client) Diagnostics(ctx context.Context, path string, version int) ([]Diagnostic, error) {
	abs, err := c.abs(path)
	if err != nil {
		return nil, err
	}

	for {
		c.mu.Lock()
		pub, ok := c.published[abs]
		updates := c.updates
		c.mu.Unlock()

		if ok && atLeast(pub.Version, version) {
			return pub.diagnostics(path), nil
		}

		select {
		case <-updates:
		case <-ctx.Done():
			if !c.inner.IsRunning() {
				return nil, fmt.Errorf("%w: %s published nothing for %s", ErrServerExited, c.server.Command, path)
			}

			return nil, fmt.Errorf("waiting for diagnostics on %s: %w", path, ctx.Err())
		}
	}
}

// Close shuts the server down over the protocol and reaps the process. The
// client is unusable afterwards.
func (c *Client) Close(ctx context.Context) error {
	shutdownErr := c.inner.Shutdown(ctx)
	exitErr := c.inner.Exit()

	c.inner.Kill()

	if err := errors.Join(shutdownErr, exitErr); err != nil {
		return fmt.Errorf("closing %s: %w", c.server.Command, err)
	}

	return nil
}

func (c *Client) onPublish(_ context.Context, _ string, params json.RawMessage) {
	pub, err := decodePublication(params)
	if err != nil {
		return
	}

	path, err := protocol.DocumentURI(pub.URI).Path()
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.published[filepath.Clean(path)] = pub

	close(c.updates)
	c.updates = make(chan struct{})
}

func (c *Client) abs(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	abs, err := filepath.Abs(filepath.Join(c.root, path))
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	return abs, nil
}

func atLeast(published *int32, want int) bool {
	return published == nil || int(*published) >= want
}
