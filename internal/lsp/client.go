package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrServerExited reports a server process that is no longer answering, which
// a caller must distinguish from a server that simply had nothing to say.
var ErrServerExited = errors.New("lsp: server exited")

// Client is one language server process for one project root. Every method is
// safe for concurrent use; documents stay open for the process's lifetime, so
// a second sync of the same file is a change rather than a re-open.
type Client struct {
	inner     *conn
	updates   chan struct{}
	versions  map[string]int
	published map[string]publication
	root      string
	server    Server
	mu        sync.Mutex
}

func newClient(ctx context.Context, root string, srv Server) (*Client, error) {
	c := &Client{
		updates:   make(chan struct{}),
		versions:  make(map[string]int),
		published: make(map[string]publication),
		server:    srv,
		root:      root,
	}

	inner, err := dial(ctx, root, srv, c.onNotify)
	if err != nil {
		return nil, fmt.Errorf("initializing %s: %w", srv.Command, err)
	}

	c.inner = inner

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

	uri := pathToURI(abs)

	c.mu.Lock()
	version, opened := c.versions[abs]
	version++
	c.versions[abs] = version
	c.mu.Unlock()

	if !opened {
		var params didOpenParams
		params.TextDocument.URI = uri
		params.TextDocument.LanguageID = c.server.Language
		params.TextDocument.Version = version
		params.TextDocument.Text = string(src)

		if err := c.inner.send(ctx, "textDocument/didOpen", params); err != nil {
			return 0, fmt.Errorf("opening %s: %w", path, err)
		}

		return version, nil
	}

	var params didChangeParams
	params.TextDocument.URI = uri
	params.TextDocument.Version = version
	params.ContentChanges = []struct {
		Text string `json:"text"`
	}{{Text: string(src)}}

	if err := c.inner.send(ctx, "textDocument/didChange", params); err != nil {
		return 0, fmt.Errorf("changing %s: %w", path, err)
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
			if !c.inner.running() {
				return nil, fmt.Errorf("%w: %s published nothing for %s", ErrServerExited, c.server.Command, path)
			}

			return nil, fmt.Errorf("waiting for diagnostics on %s: %w", path, ctx.Err())
		}
	}
}

// Close shuts the server down over the protocol and reaps the process. The
// client is unusable afterwards.
func (c *Client) Close(ctx context.Context) error {
	if err := c.inner.close(ctx); err != nil {
		return fmt.Errorf("closing %s: %w", c.server.Command, err)
	}

	return nil
}

// onNotify records a diagnostics publication and ignores everything else a
// server says on its own: the log and progress notifications are the server's
// own narration, and nothing here reads them.
func (c *Client) onNotify(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}

	pub, err := decodePublication(params)
	if err != nil {
		return
	}

	path, err := uriToPath(pub.URI)
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

	params := renameParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(abs)},
		Position:     position{Line: line, Character: column},
		NewName:      newName,
	}

	var edit workspaceEdit
	if err := c.inner.call(ctx, "textDocument/rename", params, &edit); err != nil {
		return nil, fmt.Errorf("renaming %s at %s:%d: %w", newName, path, line+1, err)
	}

	out := make(map[string][]TextEdit, len(edit.Changes))

	for uri, edits := range edit.Changes {
		file, perr := uriToPath(uri)
		if perr != nil {
			return nil, fmt.Errorf("rename touched an unreadable location: %w", perr)
		}

		for i := range edits {
			out[file] = append(out[file], textEdit(edits[i]))
		}
	}

	for _, doc := range edit.DocumentChanges {
		file, perr := uriToPath(doc.TextDocument.URI)
		if perr != nil {
			return nil, fmt.Errorf("rename touched an unreadable location: %w", perr)
		}

		for i := range doc.Edits {
			out[file] = append(out[file], textEdit(doc.Edits[i]))
		}
	}

	return out, nil
}

func textEdit(e wireTextEdit) TextEdit {
	return TextEdit{
		Line:      e.Range.Start.Line,
		Column:    e.Range.Start.Character,
		EndLine:   e.Range.End.Line,
		EndColumn: e.Range.End.Character,
		NewText:   e.NewText,
	}
}

// Reference is one place a symbol is used, as a path and a zero-based line.
type Reference struct {
	Path string
	Line int
}

// References lists every use of the symbol at path:line:column, excluding
// the declaration itself. Line and column count from zero.
//
// A caller about to remove a declaration asks this first: the language server
// answers from type information, so it counts a use in another package and
// does not count a comment that happens to spell the name.
func (c *Client) References(ctx context.Context, path string, line, column int) ([]Reference, error) {
	abs, err := c.abs(path)
	if err != nil {
		return nil, err
	}

	params := referenceParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(abs)},
		Position:     position{Line: line, Character: column},
	}

	var locations []wireLocation
	if err := c.inner.call(ctx, "textDocument/references", params, &locations); err != nil {
		return nil, fmt.Errorf("finding references to %s:%d: %w", path, line+1, err)
	}

	out := make([]Reference, 0, len(locations))

	for i := range locations {
		file, perr := uriToPath(locations[i].URI)
		if perr != nil {
			return nil, fmt.Errorf("a reference is at an unreadable location: %w", perr)
		}

		out = append(out, Reference{Path: file, Line: locations[i].Range.Start.Line})
	}

	return out, nil
}
