package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"daemontui/internal/proto"
)

// batchMsg is a coalesced group of events, sent at most once per ~16ms so a
// burst of daemon traffic costs one redraw instead of one per event.
type batchMsg []proto.Event

type connErrMsg struct{ err error }

type client struct {
	conn net.Conn
	prog *tea.Program

	mu  sync.Mutex
	buf []proto.Event
}

func sockPath() string {
	return filepath.Join(os.TempDir(), "wavezd.sock")
}

func newClient(prog *tea.Program) (*client, error) {
	c, err := net.Dial("unix", sockPath())
	if err != nil {
		return nil, err
	}
	cl := &client{conn: c, prog: prog}
	for _, name := range threadNames {
		cl.sendCmd(proto.Command{Cmd: "subscribe", Thread: name})
	}
	go cl.readLoop()
	go cl.flushLoop()
	return cl, nil
}

func (c *client) sendCmd(cmd proto.Command) {
	b, err := json.Marshal(cmd)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = c.conn.Write(b)
}

func (c *client) readLoop() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var raw struct {
			Type string `json:"type"`
		}
		line := scanner.Bytes()
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if raw.Type != "event" {
			continue
		}
		var e proto.Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		c.mu.Lock()
		c.buf = append(c.buf, e)
		c.mu.Unlock()
	}
	c.prog.Send(connErrMsg{err: scanner.Err()})
}

// flushLoop batches events on a 16ms tick to bound the client's redraw rate.
func (c *client) flushLoop() {
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		if len(c.buf) == 0 {
			c.mu.Unlock()
			continue
		}
		batch := c.buf
		c.buf = nil
		c.mu.Unlock()
		c.prog.Send(batchMsg(batch))
	}
}
