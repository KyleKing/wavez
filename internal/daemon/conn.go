package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"github.com/kyleking/wavez/internal/api"
)

const (
	readInitialBuf = 64 << 10
	readMaxBuf     = 1 << 20
	outQueueSize   = 4096
)

// conn is one client connection. Every reply and pushed event flows through
// its out channel, written by a single writer goroutine. That channel is
// never closed while a forwarder goroutine might still send to it:
// forwarders exit on stop, and wg lets teardown wait for all of them before
// out is closed.
type conn struct {
	c      net.Conn
	ctx    context.Context //nolint:containedctx // scopes every subscription this connection owns; canceled at teardown
	srv    *Server
	stop   chan struct{}
	out    chan []byte
	wake   chan struct{}
	subs   map[string]bool
	wg     sync.WaitGroup
	subsMu sync.Mutex
}

func (s *Server) handleConn(nc net.Conn) {
	defer s.connsWG.Done()

	ctx, cancel := context.WithCancel(s.ctx)

	c := &conn{
		c:    nc,
		srv:  s,
		ctx:  ctx,
		stop: make(chan struct{}),
		out:  make(chan []byte, outQueueSize),
		wake: make(chan struct{}, 1),
		subs: make(map[string]bool),
	}

	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()

	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		c.writer()
	}()

	c.wg.Add(1)
	go c.forwardPending()

	c.readLoop()

	close(c.stop)
	cancel()    // unblocks every eventlog.Subscribe forwarder mid-backlog-replay so it can exit
	c.wg.Wait() // every goroutine that can write to c.out has exited
	close(c.out)
	writerWG.Wait() // let the writer drain what's left before the socket closes

	//nolint:errcheck,gosec // best-effort teardown; the socket is going away either way
	nc.Close()
}

func (c *conn) readLoop() {
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 0, readInitialBuf), readMaxBuf)

	first := true
	for sc.Scan() {
		var cmd api.Command
		if err := json.Unmarshal(sc.Bytes(), &cmd); err != nil {
			continue
		}

		if first {
			first = false
			if cmd.Kind != api.CmdHello {
				c.reply(cmd.ID, errorReply("protocol handshake required: first command must be hello"))

				return
			}
		}

		c.handle(cmd)
	}
}

func (c *conn) writer() {
	for b := range c.out {
		if _, err := c.c.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

func (c *conn) forwardPending() {
	defer c.wg.Done()

	for {
		select {
		case <-c.wake:
			c.send(api.Reply{Kind: api.RepPending, Pending: c.srv.broker.List()})
		case <-c.stop:
			return
		}
	}
}

// send blocks until the write goroutine drains out or the connection stops.
// A reply or a subscribed backlog must never be dropped for a slow
// reader; only the underlying eventlog subscription is allowed to shed load
// (by reporting Lagged), never this connection's own delivery.
func (c *conn) send(rep api.Reply) {
	b, err := json.Marshal(rep)
	if err != nil {
		return
	}

	select {
	case c.out <- b:
	case <-c.stop:
	}
}

func (c *conn) reply(id string, rep api.Reply) {
	rep.ID = id
	c.send(rep)
}

func errorReply(msg string) api.Reply {
	return api.Reply{Kind: api.RepError, Error: msg}
}

// replyThreadInfo answers cmd with mt's synced info stamped with p's root,
// or with an error reply when the thread's log could not be read. It
// reports whether it sent info, so a caller with more work to do after the
// reply knows whether to proceed.
func (c *conn) replyThreadInfo(id string, p *Project, mt *managedThread) bool {
	info, err := mt.info()
	if err != nil {
		c.reply(id, errorReply(err.Error()))

		return false
	}
	info.Root = p.root
	c.reply(id, api.Reply{Kind: api.RepThread, Thread: &info})

	return true
}

// projectForThread answers cmd.ThreadID's Project, replying with an error
// and reporting false when no project has that thread.
func (c *conn) projectForThread(cmd api.Command) (*Project, bool) {
	p, ok := c.srv.findByThread(cmd.ThreadID)
	if !ok {
		c.reply(cmd.ID, errorReply("unknown thread"))

		return nil, false
	}

	return p, true
}

func (c *conn) handle(cmd api.Command) {
	if c.handleThreadCommand(cmd) {
		return
	}

	switch cmd.Kind {
	case api.CmdHello:
		c.reply(cmd.ID, api.Reply{Kind: api.RepHello, Protocol: api.Protocol})
	case api.CmdList:
		root := cmd.Root
		if cmd.AllRoots {
			root = ""
		}

		threads, err := c.srv.listThreads(root)
		if err != nil {
			c.reply(cmd.ID, errorReply(err.Error()))

			return
		}
		c.reply(cmd.ID, api.Reply{Kind: api.RepThreads, Threads: threads})
	case api.CmdRoutines:
		c.handleRoutines(cmd)
	case api.CmdRunRoutine:
		c.handleRunRoutine(cmd)
	default:
		c.handleMachine(cmd)
	}
}

// handleThreadCommand answers the commands that target one thread, and
// reports whether it recognized cmd.
func (c *conn) handleThreadCommand(cmd api.Command) bool {
	switch cmd.Kind {
	case api.CmdNew:
		c.handleNew(cmd)
	case api.CmdSend:
		c.handleSend(cmd)
	case api.CmdSubscribe:
		c.handleSubscribe(cmd)
	case api.CmdAnswer:
		c.handleAnswer(cmd)
	case api.CmdCancel:
		c.handleCancel(cmd)
	case api.CmdDiff:
		c.handleDiff(cmd)
	case api.CmdRestore:
		c.handleRestore(cmd)
	case api.CmdRoute:
		c.handleRoute(cmd)
	case api.CmdThink:
		c.handleThink(cmd)
	default:
		return false
	}

	return true
}

// handleMachine covers the commands about the machine rather than about a
// thread: the diagnostics panel, the schedule, and model management.
func (c *conn) handleMachine(cmd api.Command) {
	switch cmd.Kind {
	case api.CmdDiag:
		diag, err := c.srv.diagnostics()
		if err != nil {
			c.reply(cmd.ID, errorReply(err.Error()))

			return
		}
		c.reply(cmd.ID, api.Reply{Kind: api.RepDiag, Diag: &diag})
	case api.CmdSchedule:
		schedule, err := c.srv.schedule(c.ctx)
		if err != nil {
			c.reply(cmd.ID, errorReply(err.Error()))

			return
		}
		c.reply(cmd.ID, api.Reply{Kind: api.RepSchedule, Schedule: &schedule})
	case api.CmdDiagReset:
		stats, err := c.srv.aggregateFleetStats()
		if err != nil {
			c.reply(cmd.ID, errorReply(err.Error()))

			return
		}
		c.srv.window.reset(stats.rows)
		diag, err := c.srv.diagnostics()
		if err != nil {
			c.reply(cmd.ID, errorReply(err.Error()))

			return
		}
		c.reply(cmd.ID, api.Reply{Kind: api.RepDiag, Diag: &diag})
	case api.CmdModels, api.CmdModelCheck, api.CmdModelInstall, api.CmdModelRemove, api.CmdModelSettings:
		c.handleModel(cmd)
	default:
		c.reply(cmd.ID, errorReply(fmt.Sprintf("unknown command %q", cmd.Kind)))
	}
}

// handleModel runs one model command and answers with the whole list, so a
// client never merges a partial update into a list it already holds.
func (c *conn) handleModel(cmd api.Command) {
	note, err := c.srv.runModelCommand(c.ctx, cmd)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	listCtx, cancel := context.WithTimeout(c.ctx, modelListTimeout)
	defer cancel()

	models, err := c.srv.modelInfos(listCtx)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	c.reply(cmd.ID, api.Reply{Kind: api.RepModels, Models: models, Note: note})
}

func (c *conn) handleNew(cmd api.Command) {
	p, err := c.srv.resolveProject(c.ctx, cmd.Root)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	mt, err := p.mgr.create(createParams{
		Dirs: cmd.Dirs, Model: cmd.Model, Parent: cmd.Parent, Prompt: cmd.Prompt, Cycle: cmd.Cycle,
	})
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}
	c.srv.registerThread(mt.id, p)

	if !c.replyThreadInfo(cmd.ID, p, mt) {
		return
	}

	if cmd.Prompt == "" {
		return
	}
	if err := p.mgr.send(mt.id, cmd.Prompt); err != nil {
		c.reply("", errorReply(err.Error()))
	}
}

func (c *conn) handleDiff(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}

	unified, err := p.mgr.diff(context.Background(), p.differ, cmd.ThreadID)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	c.reply(cmd.ID, api.Reply{
		Kind: api.RepDiff,
		Diff: &api.Diff{ThreadID: cmd.ThreadID, Unified: unified},
	})
}

func (c *conn) handleRestore(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}

	res, err := p.mgr.restore(c.ctx, p.restorer, cmd.ThreadID, cmd.Confirm)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	c.reply(cmd.ID, api.Reply{Kind: api.RepRestore, Restore: &res})
}

func (c *conn) handleRoute(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}
	if err := p.mgr.setOverride(cmd.ThreadID, cmd.Override); err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	mt, ok := p.mgr.get(cmd.ThreadID)
	if !ok {
		return
	}
	c.replyThreadInfo(cmd.ID, p, mt)
}

func (c *conn) handleThink(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}
	if err := p.mgr.setThinking(cmd.ThreadID, cmd.Thinking); err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	mt, ok := p.mgr.get(cmd.ThreadID)
	if !ok {
		return
	}
	c.replyThreadInfo(cmd.ID, p, mt)
}

func (c *conn) handleSend(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}
	if err := p.mgr.send(cmd.ThreadID, cmd.Prompt); err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	mt, ok := p.mgr.get(cmd.ThreadID)
	if !ok {
		return
	}
	c.replyThreadInfo(cmd.ID, p, mt)
}

func (c *conn) handleCancel(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}
	if err := p.mgr.cancel(cmd.ThreadID); err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	mt, ok := p.mgr.get(cmd.ThreadID)
	if !ok {
		return
	}
	c.replyThreadInfo(cmd.ID, p, mt)
}

func (c *conn) handleAnswer(cmd api.Command) {
	if !c.srv.broker.Answer(cmd) {
		c.reply(cmd.ID, errorReply("no pending prompt with that id"))

		return
	}
	c.reply(cmd.ID, api.Reply{Kind: api.RepPending, Pending: c.srv.broker.List()})
}

func (c *conn) handleSubscribe(cmd api.Command) {
	p, ok := c.projectForThread(cmd)
	if !ok {
		return
	}

	mt, ok := p.mgr.get(cmd.ThreadID)
	if !ok {
		c.reply(cmd.ID, errorReply("unknown thread"))

		return
	}

	if !c.replyThreadInfo(cmd.ID, p, mt) {
		return
	}

	c.subsMu.Lock()
	already := c.subs[cmd.ThreadID]
	c.subs[cmd.ThreadID] = true
	c.subsMu.Unlock()

	if !already {
		c.wg.Add(1)
		go c.forwardEvents(mt, cmd.From)
	}
}

func (c *conn) forwardEvents(mt *managedThread, from uint64) {
	defer c.wg.Done()

	updates, err := mt.th.Log().Subscribe(c.ctx, from)
	if err != nil {
		c.send(api.Reply{Kind: api.RepError, Error: fmt.Sprintf("subscribing: %v", err)})

		return
	}

	for u := range updates {
		if u.Lagged {
			c.send(api.Reply{Kind: api.RepLagged, LastSeq: u.LastSeq})

			continue
		}

		ev := u.Event
		c.send(api.Reply{Kind: api.RepEvent, Event: &ev})
	}
}
