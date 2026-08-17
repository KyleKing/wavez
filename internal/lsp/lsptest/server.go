// Package lsptest runs a scripted language server so the client and the gate
// can be tested without gopls. The server is the test binary itself, re-execed
// with a script in its environment, so tests exercise real subprocess
// launching and real Content-Length framing rather than a stubbed transport.
package lsptest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/lsp"
)

// ScriptEnv names the environment variable carrying a JSON Script. A process
// that finds it set is a scripted server, not a test run.
const ScriptEnv = "WAVEZ_LSPTEST_SCRIPT"

const (
	fieldJSONRPC   = "jsonrpc"
	jsonrpcVersion = "2.0"
	startLogPerm   = 0o600
)

var errNoContentLength = errors.New("lsptest: frame without a content-length header")

// Mode changes how the scripted server misbehaves.
type Mode string

// Modes the scripted server supports.
const (
	// ModeNormal publishes the scripted diagnostics for every synced file.
	ModeNormal Mode = ""
	// ModeSilent accepts documents and publishes nothing, the shape of a
	// server that hangs on a project it cannot load.
	ModeSilent Mode = "silent"
	// ModeRefuseInitialize answers the initialize request with an error.
	ModeRefuseInitialize Mode = "refuse-initialize"
	// ModeOmitVersion publishes diagnostics without the optional version
	// field, which the protocol allows and a waiter must accept.
	ModeOmitVersion Mode = "omit-version"
)

// Diagnostic is one finding the scripted server publishes. Line is 1-based,
// matching what a caller reads off a compiler.
type Diagnostic struct {
	Message  string `json:"message"`
	Source   string `json:"source"`
	Severity int    `json:"severity"`
	Line     int    `json:"line"`
}

// Script is what a scripted server does: the diagnostics it publishes per
// file base name, and how it misbehaves.
type Script struct {
	Diagnostics map[string][]Diagnostic `json:"diagnostics"`
	Mode        Mode                    `json:"mode"`
	// StartLog is a file each server process appends a line to when it
	// starts, which is how a test proves one process served two runs.
	StartLog string `json:"start_log"`
	// Unrelated names a file in the same directory the server also publishes
	// diagnostics for on every sync, the way gopls reports on every file in a
	// package the caller never opened.
	Unrelated string `json:"unrelated"`
}

// Server returns the lsp.Server that launches this test binary as a scripted
// language server for ".go" files.
func Server(t *testing.T, script Script) lsp.Server {
	t.Helper()

	encoded, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("encoding script: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating test binary: %v", err)
	}

	return lsp.Server{
		Language:   "go",
		Command:    self,
		Extensions: []string{".go"},
		Env:        map[string]string{ScriptEnv: string(encoded)},
	}
}

// ServeIfChild runs the scripted server and never returns when this process
// was launched as one. Call it as the first statement of TestMain.
func ServeIfChild() {
	raw := os.Getenv(ScriptEnv)
	if raw == "" {
		return
	}

	var script Script
	if err := json.Unmarshal([]byte(raw), &script); err != nil {
		fmt.Fprintln(os.Stderr, "lsptest: bad script:", err)
		os.Exit(1)
	}

	if err := serve(script, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "lsptest:", err)
		os.Exit(1)
	}

	os.Exit(0)
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func serve(script Script, in io.Reader, out io.Writer) error {
	if script.StartLog != "" {
		if err := appendLine(script.StartLog, strconv.Itoa(os.Getpid())); err != nil {
			return err
		}
	}

	reader := bufio.NewReader(in)

	for {
		body, err := readFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}

		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			return fmt.Errorf("decoding request: %w", err)
		}

		done, err := respond(script, out, req)
		if err != nil {
			return err
		}

		if done {
			return nil
		}
	}
}

func respond(script Script, out io.Writer, req request) (bool, error) {
	switch req.Method {
	case "initialize":
		if script.Mode == ModeRefuseInitialize {
			return false, writeMessage(out, map[string]any{
				fieldJSONRPC: jsonrpcVersion,
				"id":         req.ID,
				"error":      map[string]any{"code": -32603, "message": "no views in this project"},
			})
		}

		return false, writeMessage(out, map[string]any{
			fieldJSONRPC: jsonrpcVersion,
			"id":         req.ID,
			"result":     map[string]any{"capabilities": map[string]any{"textDocumentSync": 1}},
		})
	case "shutdown":
		return false, writeMessage(out, map[string]any{fieldJSONRPC: jsonrpcVersion, "id": req.ID, "result": nil})
	case "exit":
		return true, nil
	case "textDocument/didOpen", "textDocument/didChange":
		return false, publish(script, out, req.Params)
	default:
		return false, nil
	}
}

func publish(script Script, out io.Writer, params json.RawMessage) error {
	if script.Mode == ModeSilent {
		return nil
	}

	// Field names are the protocol's, not this project's.
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"` //nolint:tagliatelle // LSP's own wire name
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return fmt.Errorf("decoding document params: %w", err)
	}

	if script.Unrelated != "" {
		dir := p.TextDocument.URI[:strings.LastIndex(p.TextDocument.URI, "/")+1]
		if err := publishFor(script, out, dir+script.Unrelated, 0); err != nil {
			return err
		}
	}

	return publishFor(script, out, p.TextDocument.URI, p.TextDocument.Version)
}

func publishFor(script Script, out io.Writer, uri string, version int) error {
	scripted := script.Diagnostics[baseName(uri)]
	diags := make([]map[string]any, 0, len(scripted))

	for _, d := range scripted {
		line := d.Line - 1
		if line < 0 {
			line = 0
		}

		diags = append(diags, map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": line, "character": 0},
				"end":   map[string]any{"line": line, "character": 1},
			},
			"severity": d.Severity,
			"source":   d.Source,
			"message":  d.Message,
		})
	}

	params := map[string]any{"uri": uri, "version": version, "diagnostics": diags}
	if script.Mode == ModeOmitVersion {
		delete(params, "version")
	}

	return writeMessage(out, map[string]any{
		fieldJSONRPC: jsonrpcVersion,
		"method":     "textDocument/publishDiagnostics",
		"params":     params,
	})
}

func baseName(uri string) string {
	path := strings.TrimPrefix(uri, "file://")
	if unescaped, err := url.PathUnescape(path); err == nil {
		path = unescaped
	}

	return filepath.Base(path)
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := 0

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading frame header: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}

		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("bad content-length: %w", err)
			}
		}
	}

	if length == 0 {
		return nil, errNoContentLength
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("reading frame body: %w", err)
	}

	return body, nil
}

func writeMessage(out io.Writer, msg map[string]any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}

	if _, err := fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}

	return nil
}

func appendLine(path, line string) error {
	//nolint:gosec // path is a test-owned temp file
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, startLogPerm)
	if err != nil {
		return fmt.Errorf("opening start log: %w", err)
	}
	defer f.Close() //nolint:errcheck // append-only log written by a test server

	if _, err := fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("writing start log: %w", err)
	}

	return nil
}
