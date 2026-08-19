package daemon_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/eventlog"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

func TestHandshake_ProtocolMismatchRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t, fake.New("local"))

	t.Run("hello first is accepted", func(t *testing.T) {
		t.Parallel()
		cl := dial(t, h)
		rep := cl.hello()
		if rep.Kind != api.RepHello || rep.Protocol != api.Protocol {
			t.Fatalf("hello reply = %+v", rep)
		}
	})

	t.Run("non-hello first is refused and connection closes", func(t *testing.T) {
		t.Parallel()
		cl := dial(t, h)
		cl.send(api.Command{ID: "x", Kind: api.CmdList})
		rep, ok := cl.recv()
		if !ok || rep.Kind != api.RepError {
			t.Fatalf("reply = %+v (ok=%v), want a RepError refusal", rep, ok)
		}
		if _, ok := cl.recv(); ok {
			t.Fatalf("expected the connection to close after refusal")
		}
	})
}

func TestThreads_CreateThenList(t *testing.T) {
	t.Parallel()

	h := newHarness(t, fake.New("local"))
	cl := dial(t, h)
	cl.hello()

	created := cl.newThread([]string{"/repo/a"})
	if created.ID == "" || created.Dir != "/repo/a" {
		t.Fatalf("created = %+v", created)
	}

	cl.send(api.Command{ID: "list", Kind: api.CmdList})
	rep, ok := cl.recv()
	if !ok || rep.Kind != api.RepThreads {
		t.Fatalf("list reply = %+v (ok=%v)", rep, ok)
	}
	if len(rep.Threads) != 1 || rep.Threads[0].ID != created.ID {
		t.Fatalf("threads = %+v, want exactly %q", rep.Threads, created.ID)
	}
}

// TestThreads_ListFailsWhenLogUnreadable guards the invariant sync exists to
// hold: a thread whose log cannot be read must fail the request rather than
// answer with a cached snapshot that may already be stale.
func TestThreads_ListFailsWhenLogUnreadable(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	h := newHarness(t, fake.New("local"), withServerOptions(daemon.WithLogDir(logDir)))
	cl := dial(t, h)
	cl.hello()

	created := cl.newThread([]string{"/repo/a"})

	// A freshly created thread has appended nothing yet, so its log's ring is
	// still empty and Since always reads the file on disk rather than
	// memory, which is what makes corrupting the file on disk enough to
	// force a read error on the very next sync.
	logPath := filepath.Join(logDir, created.ID+".jsonl")
	if err := os.WriteFile(logPath, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("corrupting log: %v", err)
	}

	cl.send(api.Command{ID: "list", Kind: api.CmdList})
	rep, ok := cl.recv()
	if !ok || rep.Kind != api.RepError {
		t.Fatalf("list reply = %+v (ok=%v), want an error rather than a stale thread list", rep, ok)
	}
}

// TestProjects_LoaderRoutesByRoot is the fleet lane's core claim: one Server
// built with a Loader (no WithLoop) serves several project roots, loading
// each lazily the first time a thread names it, keeping every root's
// threads apart, and routing a command naming only a thread id to that
// thread's own project without the caller repeating the root.
func TestProjects_LoaderRoutesByRoot(t *testing.T) {
	t.Parallel()

	rootA, rootB := t.TempDir(), t.TempDir()

	broker := daemon.NewBroker()
	loop := agent.New(fake.New("local"), fake.New("hosted"),
		tool.NewRegistry(echoTool{name: "echo"}), broker.Gate(), agent.WithLocalModel("qwen3:8b"))

	var (
		mu     sync.Mutex
		loaded []string
	)
	loader := func(_ context.Context, root string) (*daemon.Project, error) {
		mu.Lock()
		loaded = append(loaded, root)
		mu.Unlock()

		return daemon.NewProject(root, daemon.ProjectConfig{Loop: loop, LogDir: filepath.Join(root, "threads")})
	}

	sockPath := shortSockPath(t)
	srv, err := daemon.New(sockPath,
		daemon.WithBroker(broker), daemon.WithLoader(loader), daemon.WithShutdownGrace(2*time.Second))
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

	cl := dial(t, &testHarness{server: srv, broker: broker, sockPath: sockPath})
	cl.hello()

	cl.send(api.Command{ID: "newA", Kind: api.CmdNew, Root: rootA, Dirs: []string{rootA}})
	threadA := cl.recvFor("newA")
	cl.send(api.Command{ID: "newB", Kind: api.CmdNew, Root: rootB, Dirs: []string{rootB}})
	threadB := cl.recvFor("newB")

	if threadA.Thread == nil || threadA.Thread.Root != rootA {
		t.Fatalf("thread A root = %+v, want %q", threadA.Thread, rootA)
	}
	if threadB.Thread == nil || threadB.Thread.Root != rootB {
		t.Fatalf("thread B root = %+v, want %q", threadB.Thread, rootB)
	}
	if threadA.Thread.ID == threadB.Thread.ID {
		t.Fatalf("two projects issued the same thread id %q", threadA.Thread.ID)
	}

	assertListScoping(t, cl, rootA, threadA)

	// A command naming only the thread id, never the root, must still reach
	// the right project: threadB's id resolves through the daemon's own
	// thread-to-project index, not through anything this call passes in.
	cl.send(api.Command{ID: "route", Kind: api.CmdRoute, ThreadID: threadB.Thread.ID, Override: router.ChoiceLocal})
	routed := cl.recvFor("route")
	if routed.Thread == nil || routed.Thread.Root != rootB || routed.Thread.ID != threadB.Thread.ID {
		t.Fatalf("routed reply = %+v, want thread %q in root %q", routed.Thread, threadB.Thread.ID, rootB)
	}

	mu.Lock()
	gotLoads := append([]string(nil), loaded...)
	mu.Unlock()
	if len(gotLoads) != 2 {
		t.Fatalf("loader ran %d time(s), want exactly 2 (one per root, cached after)", len(gotLoads))
	}
}

// assertListScoping covers CmdList's three ways of naming what to return:
// no root lists every loaded project, an explicit root filters to it, and
// AllRoots overrides a Root the caller also set, which is what lets a
// client with a default root ask for the whole fleet without clearing it
// first.
func assertListScoping(t *testing.T, cl *client, rootA string, threadA api.Reply) {
	t.Helper()

	cl.send(api.Command{ID: "listAll", Kind: api.CmdList})
	all := cl.recvFor("listAll")
	if len(all.Threads) != 2 {
		t.Fatalf("list with no root filter = %d threads, want 2", len(all.Threads))
	}

	cl.send(api.Command{ID: "listA", Kind: api.CmdList, Root: rootA})
	onlyA := cl.recvFor("listA")
	if len(onlyA.Threads) != 1 || onlyA.Threads[0].ID != threadA.Thread.ID {
		t.Fatalf("list filtered by root A = %+v, want only %q", onlyA.Threads, threadA.Thread.ID)
	}

	cl.send(api.Command{ID: "listAllRoots", Kind: api.CmdList, Root: rootA, AllRoots: true})
	viaAllRoots := cl.recvFor("listAllRoots")
	if len(viaAllRoots.Threads) != 2 {
		t.Fatalf("list with AllRoots and a Root set = %d threads, want 2", len(viaAllRoots.Threads))
	}
}

// TestSubscribe_BacklogReplayInOrder guards against the spike's first bug: a
// non-blocking send with a default drop silently lost thousands of buffered
// events during backlog replay (a reconnect after ~4700 events only
// replayed 1984). A single scripted turn emits one text chunk event per
// entry, well past that threshold, and every Seq from 1..N must arrive in
// order.
func TestSubscribe_BacklogReplayInOrder(t *testing.T) {
	t.Parallel()

	const n = 5000

	local := fake.New("local", fake.Turn{Text: chunkTexts(n), StopReason: llm.StopEndTurn})
	h := newHarness(t, local)

	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	if _, ok := cl.recv(); !ok {
		t.Fatalf("send: connection closed")
	}

	sub := dial(t, h)
	sub.hello()
	sub.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	if rep, ok := sub.recv(); !ok || rep.Kind != api.RepThread {
		t.Fatalf("subscribe ack = %+v (ok=%v)", rep, ok)
	}

	var lastSeq uint64
	var events int
	for events < n {
		rep, ok := sub.recv()
		if !ok {
			t.Fatalf("connection closed after %d/%d events", events, n)
		}
		if rep.Kind != api.RepEvent || rep.Event == nil {
			continue
		}
		if rep.Event.Seq <= lastSeq {
			t.Fatalf("event Seq out of order: got %d after %d", rep.Event.Seq, lastSeq)
		}
		lastSeq = rep.Event.Seq
		events++
	}
	if events != n {
		t.Fatalf("received %d events, want %d", events, n)
	}
}

// TestSubscribe_DisconnectDoesNotAffectOtherConnection guards against the
// spike's second bug: closing a per-connection channel while a producer
// could still be mid-send panicked with "send on closed channel". Connection
// A reads only a few events from a large backlog and is then closed abruptly
// while its forwarder is very likely still mid-replay; connection B, reading
// the same backlog, must receive it in full regardless.
func TestSubscribe_DisconnectDoesNotAffectOtherConnection(t *testing.T) {
	t.Parallel()

	const n = 3000
	const readBeforeClose = 20

	local := fake.New("local", fake.Turn{Text: chunkTexts(n), StopReason: llm.StopEndTurn})
	h := newHarness(t, local)

	setup := dial(t, h)
	setup.hello()
	th := setup.newThread(nil)
	setup.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	if _, ok := setup.recv(); !ok {
		t.Fatalf("send: connection closed")
	}

	connA := dial(t, h)
	connA.hello()
	connA.send(api.Command{ID: "subA", Kind: api.CmdSubscribe, ThreadID: th.ID})
	if _, ok := connA.recv(); !ok {
		t.Fatalf("connA subscribe ack: connection closed")
	}

	connB := dial(t, h)
	connB.hello()
	connB.send(api.Command{ID: "subB", Kind: api.CmdSubscribe, ThreadID: th.ID})
	if _, ok := connB.recv(); !ok {
		t.Fatalf("connB subscribe ack: connection closed")
	}

	for range readBeforeClose {
		if _, ok := connA.recv(); !ok {
			t.Fatalf("connA: connection closed early")
		}
	}
	if err := connA.c.Close(); err != nil {
		t.Fatalf("closing connA: %v", err)
	}

	var events int
	var lastSeq uint64
	for events < n {
		rep, ok := connB.recv()
		if !ok {
			t.Fatalf("connB closed after %d/%d events", events, n)
		}
		if rep.Kind != api.RepEvent || rep.Event == nil {
			continue
		}
		if rep.Event.Seq <= lastSeq {
			t.Fatalf("connB event Seq out of order: got %d after %d", rep.Event.Seq, lastSeq)
		}
		lastSeq = rep.Event.Seq
		events++
	}
}

func TestSubscribe_ReconnectResumesFromSeq(t *testing.T) {
	t.Parallel()

	const n = 200
	const resumeFrom = 120

	local := fake.New("local", fake.Turn{Text: chunkTexts(n), StopReason: llm.StopEndTurn})
	h := newHarness(t, local)

	setup := dial(t, h)
	setup.hello()
	th := setup.newThread(nil)
	setup.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	if _, ok := setup.recv(); !ok {
		t.Fatalf("send: connection closed")
	}

	first := dial(t, h)
	first.hello()
	first.send(api.Command{ID: "sub1", Kind: api.CmdSubscribe, ThreadID: th.ID})
	if _, ok := first.recv(); !ok {
		t.Fatalf("first subscribe ack: connection closed")
	}
	var count int
	for count < n {
		rep, ok := first.recv()
		if !ok {
			t.Fatalf("first: connection closed after %d/%d events", count, n)
		}
		if rep.Kind == api.RepEvent && rep.Event != nil {
			count++
		}
	}

	second := dial(t, h)
	second.hello()
	second.send(api.Command{ID: "sub2", Kind: api.CmdSubscribe, ThreadID: th.ID, From: uint64(resumeFrom)})
	if _, ok := second.recv(); !ok {
		t.Fatalf("second subscribe ack: connection closed")
	}

	var lastSeq uint64
	var received int
	for received < n-resumeFrom {
		rep, ok := second.recv()
		if !ok {
			t.Fatalf("second: connection closed after %d/%d events", received, n-resumeFrom)
		}
		if rep.Kind != api.RepEvent || rep.Event == nil {
			continue
		}
		if rep.Event.Seq <= uint64(resumeFrom) {
			t.Fatalf("resumed subscription replayed Seq %d, want > %d", rep.Event.Seq, resumeFrom)
		}
		if lastSeq != 0 && rep.Event.Seq != lastSeq+1 {
			t.Fatalf("gap in resumed stream: %d then %d", lastSeq, rep.Event.Seq)
		}
		lastSeq = rep.Event.Seq
		received++
	}
}

// TestPending_AnsweredFromSecondConnectionResolvesOnce exercises the
// pending-prompt registry: a permission request from a thread becomes an
// api.PendingInfo visible to every connected client, and two clients racing
// to answer it must resolve it exactly once.
func TestPending_AnsweredFromSecondConnectionResolvesOnce(t *testing.T) {
	t.Parallel()

	local := fake.New("local",
		fake.Turn{
			ToolCalls:  []llm.ToolCall{{ID: "1", Name: "gated", Input: []byte(`{}`)}},
			StopReason: llm.StopToolUse,
		},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	h := newHarness(t, local, withTool(gatedTool{echoTool: echoTool{name: "gated"}, key: "gated-key"}))

	watcher := dial(t, h)
	watcher.hello()
	th := watcher.newThread(nil)
	watcher.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	watcher.recvFor("sub")

	watcher.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	watcher.recvFor("send")

	pending := waitForEvent(t, watcher, func(rep api.Reply) bool {
		return rep.Kind == api.RepPending && len(rep.Pending) == 1
	})
	promptID := pending.Pending[0].ID

	connB := dial(t, h)
	connB.hello()
	connC := dial(t, h)
	connC.hello()

	var wg sync.WaitGroup
	results := make(chan api.Reply, 2)
	start := make(chan struct{})
	for _, cl := range []*client{connB, connC} {
		wg.Add(1)
		go func(cl *client) {
			defer wg.Done()
			<-start
			cl.send(api.Command{ID: "answer", Kind: api.CmdAnswer, PromptID: promptID, Decision: permission.Allow})
			results <- cl.recvFor("answer")
		}(cl)
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, failures int
	for rep := range results {
		switch rep.Kind {
		case api.RepPending:
			successes++
		case api.RepError:
			failures++
		default:
			t.Errorf("unexpected answer reply kind %q", rep.Kind)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("successes=%d failures=%d, want exactly one of each", successes, failures)
	}

	waitForEvent(t, watcher, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil &&
			rep.Event.Kind == event.KindState && rep.Event.State == event.StateDone
	})
}

// TestConnections_ConcurrentSubscribeCycles reproduces the spike's load test
// that surfaced the "send on closed channel" panic: 400 rapid
// connect/subscribe/disconnect cycles under -race, against a thread with an
// actively streaming backlog so a forwarder is likely to be mid-send when
// its connection closes.
func TestConnections_ConcurrentSubscribeCycles(t *testing.T) {
	t.Parallel()

	const cycles = 400
	const workers = 20

	local := fake.New("local", fake.Turn{Text: chunkTexts(500), StopReason: llm.StopEndTurn})
	h := newHarness(t, local)

	setup := dial(t, h)
	setup.hello()
	th := setup.newThread(nil)
	setup.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	if _, ok := setup.recv(); !ok {
		t.Fatalf("send: connection closed")
	}

	var wg sync.WaitGroup
	perWorker := cycles / workers
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWorker {
				cycleConnectSubscribeDisconnect(t, h, th.ID)
			}
		}()
	}
	wg.Wait()
}

func cycleConnectSubscribeDisconnect(t *testing.T, h *testHarness, threadID string) {
	t.Helper()

	var d net.Dialer
	c, err := d.DialContext(context.Background(), "unix", h.sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() {
		//nolint:errcheck,gosec // exercising abrupt disconnect; the close error itself is not the point
		c.Close()
	}()

	if err := c.SetDeadline(time.Now().Add(testDeadline)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := c.Write([]byte(`{"id":"h","kind":"hello"}` + "\n")); err != nil {
		return
	}
	buf := make([]byte, 4096)
	if _, err := c.Read(buf); err != nil {
		return
	}
	cmd := `{"id":"s","kind":"subscribe","thread_id":"` + threadID + `"}` + "\n"
	if _, err := c.Write([]byte(cmd)); err != nil {
		return
	}
	_, _ = c.Read(buf) //nolint:errcheck // best-effort read before the abrupt disconnect below
}

type fakeStats struct{ mem daemon.MachineStats }

func (f fakeStats) Stats() daemon.MachineStats { return f.mem }

func TestDiagnostics_ReflectsThreadCountsAndInjectedStats(t *testing.T) {
	t.Parallel()

	broker := daemon.NewBroker()
	loop := agentLoopForTest(t, broker)
	sockPath := shortSockPath(t)
	stats := fakeStats{mem: daemon.MachineStats{UsedBytes: 111, TotalBytes: 222, ModelBytes: 33, ModelMeasured: true}}

	srv, err := daemon.New(sockPath,
		daemon.WithLoop(loop),
		daemon.WithBroker(broker),
		daemon.WithLogDir(t.TempDir()),
		daemon.WithStatsSource(stats),
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

	cl := dial(t, &testHarness{sockPath: sockPath})
	cl.hello()
	cl.newThread(nil)
	cl.newThread(nil)

	cl.send(api.Command{ID: "diag", Kind: api.CmdDiag})
	rep := cl.recvFor("diag")
	if rep.Kind != api.RepDiag || rep.Diag == nil {
		t.Fatalf("diag reply = %+v", rep)
	}
	if rep.Diag.Threads != 2 {
		t.Fatalf("Diag.Threads = %d, want 2", rep.Diag.Threads)
	}
	if rep.Diag.MemUsedBytes != 111 || rep.Diag.MemTotalBytes != 222 || rep.Diag.ModelBytes != 33 {
		t.Fatalf("Diag mem fields = %+v, want the injected stats", rep.Diag)
	}
}

func TestCancel_StopsInFlightTurn(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{
		Text:       chunkTexts(50),
		StopReason: llm.StopEndTurn,
		Delay:      5 * time.Millisecond,
	})
	h := newHarness(t, local)

	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)

	cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	cl.recvFor("sub")

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	cl.recvFor("send")

	// Let at least one chunk land so the turn is genuinely in flight before canceling.
	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil && rep.Event.Kind == event.KindAgent
	})

	cl.send(api.Command{ID: "cancel", Kind: api.CmdCancel, ThreadID: th.ID})
	if rep := cl.recvFor("cancel"); rep.Kind != api.RepThread {
		t.Fatalf("cancel reply = %+v", rep)
	}

	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil &&
			rep.Event.Kind == event.KindState && rep.Event.State == event.StateIdle
	})
}

func TestListen_StaleSocketFileIsReplaced(t *testing.T) {
	t.Parallel()

	sockPath := shortSockPath(t)

	var lc net.ListenConfig
	stale, err := lc.Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatalf("creating stale listener: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("closing stale listener (file left behind): %v", err)
	}

	broker := daemon.NewBroker()
	loop := agentLoopForTest(t, broker)
	srv, err := daemon.New(sockPath,
		daemon.WithLoop(loop),
		daemon.WithBroker(broker),
		daemon.WithLogDir(t.TempDir()),
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

	var d net.Dialer
	c, err := d.DialContext(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatalf("dial after stale socket replacement: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("closing connection: %v", err)
	}
}

var errNoJJRepo = errors.New("capturing checkpoint: /x is not a jj repository")

type failingCheckpointer struct{ err error }

func (f failingCheckpointer) Capture(context.Context, string) (string, error) { return "", f.err }
func (f failingCheckpointer) Restore(context.Context, string, string) error   { return f.err }

func TestSend_RunErrorReachesTheThreadLog(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"never"}, StopReason: llm.StopEndTurn})
	cp := failingCheckpointer{err: errNoJJRepo}
	h := newHarness(t, local, withLoopOptions(agent.WithCheckpointer(cp, "/x")))

	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)

	cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	cl.recvFor("sub")

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	cl.recvFor("send")

	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil &&
			rep.Event.Kind == event.KindState && rep.Event.State == event.StateFailed
	})
	errEv := waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil && rep.Event.Kind == event.KindError
	})
	if !strings.Contains(errEv.Event.Text, "not a jj repository") {
		t.Fatalf("error event text = %q, want the checkpoint failure", errEv.Event.Text)
	}

	cl.send(api.Command{ID: "list", Kind: api.CmdList})
	rep := cl.recvFor("list")
	if len(rep.Threads) != 1 {
		t.Fatalf("threads = %+v, want one", rep.Threads)
	}
	if got := rep.Threads[0]; got.State != event.StateFailed || !strings.Contains(got.Step, "not a jj repository") {
		t.Fatalf("thread info after a failed run = %+v", got)
	}
}

// TestList_NeverReportsStateOlderThanTheStream guards the ordering invariant
// every client relies on: once an event is visible to a subscriber, any
// subsequent command answered by the daemon must reflect at least that much
// state. It issues CmdList on a second, freshly dialed connection the moment
// the first connection's subscription delivers the terminal state, with no
// wait in between, so a list answered from a cache that lags the log shows up
// as State:idle rather than the failed state the stream already reported.
func TestList_NeverReportsStateOlderThanTheStream(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"never"}, StopReason: llm.StopEndTurn})
	cp := failingCheckpointer{err: errNoJJRepo}
	h := newHarness(t, local, withLoopOptions(agent.WithCheckpointer(cp, "/x")))

	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)

	cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	cl.recvFor("sub")

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	cl.recvFor("send")

	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil &&
			rep.Event.Kind == event.KindState && rep.Event.State == event.StateFailed
	})

	other := dial(t, h)
	other.hello()
	other.send(api.Command{ID: "list", Kind: api.CmdList})
	rep := other.recvFor("list")
	if len(rep.Threads) != 1 {
		t.Fatalf("threads = %+v, want one", rep.Threads)
	}
	if got := rep.Threads[0].State; got != event.StateFailed {
		t.Fatalf("state on a fresh connection's list = %q, want %q (already on the stream)", got, event.StateFailed)
	}
}

// writeThreadLog writes a real, replayable event log for id under dir: the
// same eventlog.Open/Append/Close path a running daemon uses, so Seq and
// framing match what reopen must parse.
func writeThreadLog(t *testing.T, dir, id string, events ...event.Event) {
	t.Helper()

	log, err := eventlog.Open(dir, id, eventlog.Options{})
	if err != nil {
		t.Fatalf("opening log %s: %v", id, err)
	}
	for i := range events {
		if _, err := log.Append(events[i]); err != nil {
			t.Fatalf("appending to log %s: %v", id, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatalf("closing log %s: %v", id, err)
	}
}

// TestProjects_ReopensThreadLogsOnLoad is the restart lane's core claim: a
// project loaded over a directory of pre-existing thread logs lists them on
// Home, oldest first, with an interrupted turn normalized back to idle, and
// a reopened thread is a live thread rather than a static row.
func TestProjects_ReopensThreadLogsOnLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeThreadLog(t, dir, "thread-a",
		event.Event{Kind: event.KindUser, Text: "fix the flaky lease test"},
		event.Event{Kind: event.KindState, State: event.StateDone},
	)
	time.Sleep(2 * time.Millisecond) // so B's first event sorts strictly after A's
	writeThreadLog(t, dir, "thread-b",
		event.Event{Kind: event.KindUser, Text: "say hi"},
		event.Event{Kind: event.KindState, State: event.StateWorking},
	)

	local := fake.New("local", fake.Turn{Text: []string{"hi"}, StopReason: llm.StopEndTurn})
	h := newHarness(t, local, withServerOptions(daemon.WithLogDir(dir)))
	cl := dial(t, h)
	cl.hello()

	cl.send(api.Command{ID: "list", Kind: api.CmdList})
	rep := cl.recvFor("list")
	if rep.Kind != api.RepThreads || len(rep.Threads) != 2 {
		t.Fatalf("list reply = %+v, want two threads", rep)
	}

	a, b := rep.Threads[0], rep.Threads[1]
	if a.ID != "thread-a" || a.Name != "fix-the-flaky-lease-test" || a.State != event.StateDone {
		t.Fatalf("thread A = %+v", a)
	}
	if b.ID != "thread-b" || b.Name != "say-hi" || b.State != event.StateIdle {
		t.Fatalf("thread B = %+v, want idle (an interrupted turn normalized on load)", b)
	}

	cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: "thread-b"})
	cl.recvFor("sub")

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: "thread-b", Prompt: "go"})
	cl.recvFor("send")

	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil &&
			rep.Event.Kind == event.KindState && rep.Event.State == event.StateDone
	})
}
