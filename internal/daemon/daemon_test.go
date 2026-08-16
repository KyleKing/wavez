package daemon_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
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
	h := newHarness(t, local, gatedTool{echoTool: echoTool{name: "gated"}, key: "gated-key"})

	watcher := dial(t, h)
	watcher.hello()
	th := watcher.newThread(nil)
	watcher.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	if _, ok := watcher.recv(); !ok {
		t.Fatalf("subscribe ack: connection closed")
	}

	watcher.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	if _, ok := watcher.recv(); !ok {
		t.Fatalf("send: connection closed")
	}

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

type fakeStats struct{ mem daemon.MemStats }

func (f fakeStats) Stats() daemon.MemStats { return f.mem }

func TestDiagnostics_ReflectsThreadCountsAndInjectedStats(t *testing.T) {
	t.Parallel()

	broker := daemon.NewBroker()
	loop := agentLoopForTest(t, broker)
	sockPath := shortSockPath(t)
	stats := fakeStats{mem: daemon.MemStats{UsedBytes: 111, TotalBytes: 222, ModelBytes: 33}}

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
	if _, ok := cl.recv(); !ok {
		t.Fatalf("subscribe ack: connection closed")
	}

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	if _, ok := cl.recv(); !ok {
		t.Fatalf("send: connection closed")
	}

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
