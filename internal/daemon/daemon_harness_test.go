package daemon_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
)

const testDeadline = 10 * time.Second

type echoTool struct{ name string }

func (e echoTool) Name() string          { return e.name }
func (echoTool) Description() string     { return "echoes its input" }
func (echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (echoTool) Run(_ context.Context, input json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok:" + string(input)}, nil
}

type gatedTool struct {
	echoTool
	key string
}

func (g gatedTool) RequestPermission(json.RawMessage) (permission.Request, bool) {
	return permission.Request{Tool: g.name, Action: "write", Key: g.key}, true
}

// testHarness wires a daemon.Server against fake providers for a test, and
// stops it in t.Cleanup.
type testHarness struct {
	server   *daemon.Server
	broker   *daemon.Broker
	sockPath string
}

func newHarness(t *testing.T, local *fake.Provider, extraTools ...tool.Tool) *testHarness {
	t.Helper()

	broker := daemon.NewBroker()
	tools := append([]tool.Tool{echoTool{name: "echo"}}, extraTools...)
	reg := tool.NewRegistry(tools...)
	hosted := fake.New("hosted")
	loop := agent.New(local, hosted, reg, broker.Gate())

	sockPath := shortSockPath(t)
	srv, err := daemon.New(sockPath,
		daemon.WithLoop(loop),
		daemon.WithBroker(broker),
		daemon.WithLogDir(t.TempDir()),
		daemon.WithPrefix(agent.Prefix{System: "test"}),
		daemon.WithShutdownGrace(2*time.Second),
	)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serveErr; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	waitForSocket(t, sockPath)

	return &testHarness{server: srv, broker: broker, sockPath: sockPath}
}

// shortSockPath returns a socket path in its own directory, independent of
// t.TempDir()'s test-name-derived path: a unix socket path is limited to
// ~104 bytes on macOS, and a nested subtest name easily blows past that.
func shortSockPath(t *testing.T) string {
	t.Helper()

	//nolint:usetesting // t.TempDir()'s test-name-derived path is exactly what this must avoid
	dir, err := os.MkdirTemp("", "wz")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		//nolint:errcheck,gosec // best-effort cleanup
		os.RemoveAll(dir)
	})

	return filepath.Join(dir, "d.sock")
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(testDeadline)
	for time.Now().Before(deadline) {
		d := net.Dialer{Timeout: 50 * time.Millisecond}
		c, err := d.DialContext(context.Background(), "unix", path)
		if err == nil {
			//nolint:errcheck,gosec // probe connection
			c.Close()

			return
		}
	}
	t.Fatalf("socket %s never became available", path)
}

// client is a minimal, deterministic test client over the daemon's
// newline-delimited JSON protocol.
type client struct {
	t  *testing.T
	c  net.Conn
	sc *bufio.Scanner
}

func dial(t *testing.T, h *testHarness) *client {
	t.Helper()

	var d net.Dialer
	c, err := d.DialContext(context.Background(), "unix", h.sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		//nolint:errcheck,gosec // best-effort cleanup
		c.Close()
	})

	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	return &client{t: t, c: c, sc: sc}
}

func (cl *client) send(cmd api.Command) {
	cl.t.Helper()

	b, err := json.Marshal(cmd)
	if err != nil {
		cl.t.Fatalf("marshal command: %v", err)
	}
	if err := cl.c.SetWriteDeadline(time.Now().Add(testDeadline)); err != nil {
		cl.t.Fatalf("set write deadline: %v", err)
	}
	if _, err := cl.c.Write(append(b, '\n')); err != nil {
		cl.t.Fatalf("write command: %v", err)
	}
}

func (cl *client) recv() (api.Reply, bool) {
	cl.t.Helper()

	if err := cl.c.SetReadDeadline(time.Now().Add(testDeadline)); err != nil {
		cl.t.Fatalf("set read deadline: %v", err)
	}
	if !cl.sc.Scan() {
		return api.Reply{}, false
	}

	var rep api.Reply
	if err := json.Unmarshal(cl.sc.Bytes(), &rep); err != nil {
		cl.t.Fatalf("unmarshal reply: %v", err)
	}

	return rep, true
}

// recvFor reads replies until one echoes id, skipping any unsolicited
// broadcast (Pending has no ID) that interleaves with it.
func (cl *client) recvFor(id string) api.Reply {
	cl.t.Helper()

	for {
		rep, ok := cl.recv()
		if !ok {
			cl.t.Fatalf("recvFor %q: connection closed", id)
		}
		if rep.ID == id {
			return rep
		}
	}
}

func (cl *client) hello() api.Reply {
	cl.t.Helper()
	cl.send(api.Command{ID: "hello", Kind: api.CmdHello})
	rep, ok := cl.recv()
	if !ok {
		cl.t.Fatalf("hello: connection closed")
	}

	return rep
}

func (cl *client) newThread(dirs []string) api.ThreadInfo {
	cl.t.Helper()
	cl.send(api.Command{ID: "new", Kind: api.CmdNew, Dirs: dirs})
	rep, ok := cl.recv()
	if !ok || rep.Thread == nil {
		cl.t.Fatalf("new: unexpected reply %+v (ok=%v)", rep, ok)
	}

	return *rep.Thread
}

// waitForEvent reads replies on cl (already subscribed) until pred matches
// one, or the read deadline trips.
func waitForEvent(t *testing.T, cl *client, pred func(api.Reply) bool) api.Reply {
	t.Helper()

	for {
		rep, ok := cl.recv()
		if !ok {
			t.Fatalf("waitForEvent: connection closed before match")
		}
		if pred(rep) {
			return rep
		}
	}
}

func agentLoopForTest(t *testing.T, broker *daemon.Broker) *agent.Loop {
	t.Helper()

	reg := tool.NewRegistry(echoTool{name: "echo"})
	local := fake.New("local")
	hosted := fake.New("hosted")

	return agent.New(local, hosted, reg, broker.Gate())
}

func chunkTexts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("chunk-%d", i)
	}

	return out
}
