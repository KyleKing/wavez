package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"

	"github.com/sourcegraph/jsonrpc2"
)

// errNotAFileURI reports a location a server named with a scheme this client
// cannot read a file from.
var errNotAFileURI = errors.New("lsp: not a file URI")

// errClosed reports a connection whose server has been shut down.
var errClosed = errors.New("lsp: connection is closed")

// conn is one language server subprocess and the JSON-RPC connection to it.
//
// It talks to jsonrpc2 directly rather than through a client library because
// the one wavez used answered every server request whose id was zero as if it
// were a notification, which is a null result: ty numbers its requests from
// zero, asks for its configuration first, and answers nothing at all once it
// gets one.
type conn struct {
	rpc      *jsonrpc2.Conn
	cmd      *exec.Cmd
	notify   func(method string, params json.RawMessage)
	folders  []map[string]any
	shutdown atomic.Bool
}

// dial starts srv's process under root and completes the LSP handshake. The
// notify callback receives every notification the server sends, on the
// connection's own goroutine, so it must not block.
func dial(ctx context.Context, root string, srv Server, notify func(string, json.RawMessage)) (*conn, error) {
	// The process outlives dial's context the way the connection does, so it
	// is started detached from it and ended by close or kill.
	//nolint:gosec,noctx // the command comes from this project's own server table
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Dir = root

	if len(srv.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range srv.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("piping stdin to %s: %w", srv.Command, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("piping stdout from %s: %w", srv.Command, err)
	}

	// A server's stderr is its own log, and a language server logs on every
	// document it loads. Nothing here reads it, so it goes nowhere rather
	// than filling the thread's own output.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", srv.Command, err)
	}

	c := &conn{cmd: cmd, notify: notify, folders: []map[string]any{
		{"uri": pathToURI(root), "name": filepath.Base(root)},
	}}

	stream := jsonrpc2.NewBufferedStream(pipe{r: stdout, w: stdin}, jsonrpc2.VSCodeObjectCodec{})
	// The connection outlives this call: it serves every later request on the
	// client, and is ended by close or kill rather than by whoever dialed.
	//nolint:contextcheck // deliberately not the dialer's context
	c.rpc = jsonrpc2.NewConn(context.Background(), stream, jsonrpc2.HandlerWithError(c.handle).SuppressErrClosed())

	if err := c.initialize(ctx, root); err != nil {
		c.kill()

		return nil, err
	}

	return c, nil
}

// pipe joins a subprocess's stdout and stdin into the single stream jsonrpc2
// reads and writes.
type pipe struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p pipe) Read(b []byte) (int, error)  { return p.r.Read(b) }  //nolint:wrapcheck // a pass-through pipe
func (p pipe) Write(b []byte) (int, error) { return p.w.Write(b) } //nolint:wrapcheck // a pass-through pipe

func (p pipe) Close() error {
	return errors.Join(p.w.Close(), p.r.Close())
}

func (c *conn) initialize(ctx context.Context, root string) error {
	params := map[string]any{
		"processId":             os.Getpid(),
		"clientInfo":            map[string]any{"name": "wavez"},
		"rootUri":               pathToURI(root),
		"capabilities":          clientCapabilities(),
		"workspaceFolders":      c.folders,
		"initializationOptions": nil,
		"trace":                 "off",
	}

	var result json.RawMessage
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}

	if err := c.rpc.Notify(ctx, "initialized", map[string]any{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

// handle answers what a server asks of its client. Everything it asks that
// wavez has no answer for is refused rather than left pending, since a server
// waiting on a reply that never comes stops serving requests of its own.
func (c *conn) handle(_ context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
	if req.Notif {
		if c.notify != nil && req.Params != nil {
			c.notify(req.Method, *req.Params)
		}

		return nil, nil //nolint:nilnil // a notification has no reply
	}

	switch req.Method {
	case "workspace/configuration":
		return configuration(req)
	case "workspace/workspaceFolders":
		return c.folders, nil
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		return nil, nil //nolint:nilnil // accepted with an empty result, which is what the protocol asks for
	default:
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, Message: req.Method}
	}
}

// configuration answers with one empty settings object per requested item.
// No server is configured through settings here, and the answer still has to
// be a list of the right length: a server that asks for two sections and is
// handed one value has no way to tell which it got.
func configuration(req *jsonrpc2.Request) (any, error) {
	var params configurationParams
	if req.Params != nil {
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, fmt.Errorf("decoding a configuration request: %w", err)
		}
	}

	answer := make([]any, len(params.Items))
	for i := range answer {
		answer[i] = map[string]any{}
	}

	return answer, nil
}

// call sends a request and decodes its result, or reports the server's error.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	if c.shutdown.Load() {
		return fmt.Errorf("%s: %w", method, errClosed)
	}

	if err := c.rpc.Call(ctx, method, params, result); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	return nil
}

func (c *conn) send(ctx context.Context, method string, params any) error {
	if c.shutdown.Load() {
		return fmt.Errorf("%s: %w", method, errClosed)
	}

	if err := c.rpc.Notify(ctx, method, params); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	return nil
}

// running reports a server process that has not exited. A caller that waited
// out its own deadline asks this to tell a slow server from a dead one.
func (c *conn) running() bool {
	select {
	case <-c.rpc.DisconnectNotify():
		return false
	default:
		return !c.shutdown.Load()
	}
}

// close shuts the server down over the protocol and reaps the process. A
// server that answers shutdown by closing the connection has done what was
// asked, so that is not reported as a failure.
func (c *conn) close(ctx context.Context) error {
	if c.shutdown.Swap(true) {
		return nil
	}

	var result json.RawMessage

	err := c.rpc.Call(ctx, "shutdown", nil, &result)
	if err != nil && !errors.Is(err, jsonrpc2.ErrClosed) {
		err = fmt.Errorf("shutdown: %w", err)
	} else {
		err = nil
	}

	if nerr := c.rpc.Notify(ctx, "exit", nil); nerr != nil && !errors.Is(nerr, jsonrpc2.ErrClosed) {
		err = errors.Join(err, fmt.Errorf("exit: %w", nerr))
	}

	c.kill()

	return err
}

// kill ends the process without asking. It is what a failed handshake and a
// completed shutdown both finish with, so no server is left running after its
// client is gone.
func (c *conn) kill() {
	c.shutdown.Store(true)

	_ = c.rpc.Close() //nolint:errcheck // killing a server reports nothing a caller can act on

	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill() //nolint:errcheck // the process may already be gone
	}

	_ = c.cmd.Wait() //nolint:errcheck // the exit status of a killed server says nothing
}
