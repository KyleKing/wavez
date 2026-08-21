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

// TextEdit is one replacement inside one file, in the line and column
// numbering the server speaks. Line and Column count from zero, and Column
// counts UTF-16 code units rather than bytes or runes, which is what the
// protocol specifies and what a caller applying the edit has to honor.
type TextEdit struct {
	NewText   string
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

// Rename asks the server for every edit that renames the symbol at
// path:line:column to newName, keyed by absolute file path. Line and column
// count from zero.
//
// The server decides what a rename means, which is the point: it follows the
// symbol through the type information rather than through the text, so an
// unrelated identifier that happens to share the name is left alone and a
// use in another package is not. A server that refuses the rename (a keyword,
// a symbol in a dependency) answers with an error rather than an empty edit.
func (c *Client) Rename(
	ctx context.Context, path string, line, column int, newName string,
) (map[string][]TextEdit, error) {
	abs, err := c.abs(path)
	if err != nil {
		return nil, err
	}

	edit, err := c.inner.RequestRename(ctx, abs, line, column, newName)
	if err != nil {
		return nil, fmt.Errorf("renaming %s at %s:%d: %w", newName, path, line+1, err)
	}

	out := make(map[string][]TextEdit, len(edit.Changes))

	for uri, edits := range edit.Changes {
		file, perr := uri.Path()
		if perr != nil {
			return nil, fmt.Errorf("rename touched an unreadable location %q: %w", uri, perr)
		}

		for i := range edits {
			out[file] = append(out[file], textEdit(edits[i]))
		}
	}

	for _, doc := range edit.DocumentChanges {
		if doc.TextDocumentEdit == nil {
			continue
		}

		file, perr := doc.TextDocumentEdit.TextDocument.URI.Path()
		if perr != nil {
			return nil, fmt.Errorf("rename touched an unreadable location: %w", perr)
		}

		for _, e := range doc.TextDocumentEdit.Edits {
			te, ok, terr := asTextEdit(e)
			if terr != nil {
				return nil, terr
			}

			if ok {
				out[file] = append(out[file], te)
			}
		}
	}

	return out, nil
}

// asTextEdit pulls a plain TextEdit out of the protocol's union type. The
// union decodes into an interface holding whatever JSON shape arrived, so a
// type assertion sees a map rather than the struct, and the annotated variant
// (which carries a change annotation this caller has nothing to do with) is
// skipped rather than misread as a plain edit.
func asTextEdit(e protocol.Or_TextDocumentEdit_edits_Elem) (TextEdit, bool, error) {
	raw, err := json.Marshal(e.Value)
	if err != nil {
		return TextEdit{}, false, fmt.Errorf("re-encoding a rename edit: %w", err)
	}

	//nolint:tagliatelle // the field name is the protocol's, not this project's
	var probe struct {
		AnnotationID *string `json:"annotationId"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.AnnotationID != nil {
		return TextEdit{}, false, nil
	}

	var te protocol.TextEdit
	if err := json.Unmarshal(raw, &te); err != nil {
		return TextEdit{}, false, fmt.Errorf("decoding a rename edit: %w", err)
	}

	return textEdit(te), true, nil
}

func textEdit(e protocol.TextEdit) TextEdit {
	return TextEdit{
		Line:      int(e.Range.Start.Line),
		Column:    int(e.Range.Start.Character),
		EndLine:   int(e.Range.End.Line),
		EndColumn: int(e.Range.End.Character),
		NewText:   e.NewText,
	}
}
