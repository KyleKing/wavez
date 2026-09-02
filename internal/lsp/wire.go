package lsp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// The protocol structures this client sends and decodes. Only the fields
// wavez reads are declared: a server may send more, and everything it sends
// that is not named here is dropped rather than round-tripped.
//
//nolint:tagliatelle // every wire name below is LSP's, not this project's
type (
	position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}

	textRange struct {
		Start position `json:"start"`
		End   position `json:"end"`
	}

	wireTextEdit struct {
		NewText string    `json:"newText"`
		Range   textRange `json:"range"`
	}

	wireLocation struct {
		URI   string    `json:"uri"`
		Range textRange `json:"range"`
	}

	// A workspace edit carries the same edits two ways: `changes` keys them
	// by URI, and `documentChanges` versions each file. A server answers with
	// one or the other, so both are read.
	workspaceEdit struct {
		Changes         map[string][]wireTextEdit `json:"changes"`
		DocumentChanges []documentChange          `json:"documentChanges"`
	}

	documentChange struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		// An element is a TextEdit or an AnnotatedTextEdit, which differ only
		// by a field this caller has nothing to do with, so both decode here
		// and the annotation is ignored.
		Edits []wireTextEdit `json:"edits"`
	}

	textDocumentIdentifier struct {
		URI string `json:"uri"`
	}

	renameParams struct {
		NewName      string                 `json:"newName"`
		TextDocument textDocumentIdentifier `json:"textDocument"`
		Position     position               `json:"position"`
	}

	referenceParams struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
		Position     position               `json:"position"`
		Context      struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}

	didOpenParams struct {
		TextDocument struct {
			URI        string `json:"uri"`
			LanguageID string `json:"languageId"`
			Text       string `json:"text"`
			Version    int    `json:"version"`
		} `json:"textDocument"`
	}

	didChangeParams struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}

	configurationParams struct {
		Items []json.RawMessage `json:"items"`
	}
)

// clientCapabilities is what wavez tells a server it can do. It is the
// smallest set that reaches the three things wavez asks for (published
// diagnostics, rename, references), because a capability advertised is a
// request the server may then send, and every such request has to be
// answered here.
func clientCapabilities() map[string]any {
	return map[string]any{
		"textDocument": map[string]any{
			"synchronization":    map[string]any{"didSave": false},
			"publishDiagnostics": map[string]any{"versionSupport": true},
			"rename":             map[string]any{"prepareSupport": false},
			"references":         map[string]any{},
		},
		"workspace": map[string]any{
			"configuration":    true,
			"workspaceFolders": true,
			"workspaceEdit":    map[string]any{"documentChanges": true},
		},
		"general": map[string]any{"positionEncodings": []string{"utf-16"}},
	}
}

// pathToURI renders an absolute path as a file URI, escaping each segment the
// way url.URL does, so a path holding a space or a percent survives the round
// trip through a server.
func pathToURI(abs string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}

	return u.String()
}

// uriToPath is pathToURI's inverse for the file scheme, which is the only one
// a server may name a location in here.
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parsing %q as a URI: %w", uri, err)
	}

	if u.Scheme != "" && u.Scheme != "file" {
		return "", fmt.Errorf("%w: %s", errNotAFileURI, uri)
	}

	path := u.Path
	if path == "" {
		path = strings.TrimPrefix(uri, "file://")
	}

	return filepath.Clean(filepath.FromSlash(path)), nil
}
