// Command wavezd is a spike daemon: it holds fake agent threads and streams
// their events to any number of connected wavez clients over a unix socket.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"daemontui/internal/proto"
)

var sentences = []string{
	"reading the config file to find the build target",
	"grepping for the failing test across the repo",
	"drafting a patch to fix the null pointer",
	"running the test suite to confirm the fix",
	"summarizing the diff for the pull request",
}

var tools = []string{"grep", "read_file", "run_tests", "git_diff", "edit_file"}

type thread struct {
	name string

	mu      sync.Mutex
	backlog []proto.Event
	seq     int
	subs    map[chan proto.Event]struct{}
	pending bool

	answerCh chan string
}

func newThread(name string) *thread {
	return &thread{name: name, subs: map[chan proto.Event]struct{}{}, answerCh: make(chan string, 1)}
}

func (t *thread) emit(kind, text string) proto.Event {
	t.mu.Lock()
	t.seq++
	e := proto.Event{Type: "event", Thread: t.name, Kind: kind, Text: text, Seq: t.seq}
	t.backlog = append(t.backlog, e)
	subs := make([]chan proto.Event, 0, len(t.subs))
	for ch := range t.subs {
		subs = append(subs, ch)
	}
	t.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop rather than stall the generator
		}
	}
	return e
}

func (t *thread) subscribe() (chan proto.Event, []proto.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := make(chan proto.Event, 4096)
	t.subs[ch] = struct{}{}
	return ch, append([]proto.Event(nil), t.backlog...)
}

func (t *thread) unsubscribe(ch chan proto.Event) {
	t.mu.Lock()
	delete(t.subs, ch)
	t.mu.Unlock()
}

func (t *thread) answer(v string) {
	select {
	case t.answerCh <- v:
	default:
	}
}

// run generates fake streaming events at 20-50/sec until stop is closed.
func (t *thread) run(stop <-chan struct{}) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(len(t.name))))
	step := 0
	for {
		rate := 20 + rng.Intn(31)
		interval := time.Second / time.Duration(rate)
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}

		step++
		switch {
		case step%37 == 0:
			t.mu.Lock()
			t.pending = true
			t.mu.Unlock()
			t.emit(proto.KindPermission, "allow running tests? (y/n)")
			select {
			case v := <-t.answerCh:
				t.emit(proto.KindAgent, fmt.Sprintf("permission answered: %s", v))
			case <-time.After(5 * time.Second):
				t.emit(proto.KindAgent, "permission timed out, continuing")
			case <-stop:
				return
			}
			t.mu.Lock()
			t.pending = false
			t.mu.Unlock()
		case step%11 == 0:
			t.emit(proto.KindTool, tools[rng.Intn(len(tools))])
		case step%23 == 0:
			t.emit(proto.KindGate, "step complete")
		default:
			words := sentences[rng.Intn(len(sentences))]
			t.emit(proto.KindAgent, words)
		}
	}
}

type conn struct {
	c         net.Conn
	out       chan []byte
	stop      chan struct{}
	wg        sync.WaitGroup
	threads   map[string]*thread
	subChans  map[string]chan proto.Event
	subChansM sync.Mutex
}

func (cn *conn) writer() {
	for b := range cn.out {
		if _, err := cn.c.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

// send blocks until the connection's writer goroutine drains cn.out. Backlog
// replay and command replies must never be dropped; only the live fan-out in
// thread.emit is allowed to shed load for a slow subscriber.
func (cn *conn) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	cn.out <- b
}

func (cn *conn) forward(name string, t *thread) {
	defer cn.wg.Done()
	ch, backlog := t.subscribe()
	cn.subChansM.Lock()
	cn.subChans[name] = ch
	cn.subChansM.Unlock()

	for _, e := range backlog {
		cn.send(e)
	}
	// ch is never closed (see thread.unsubscribe): a concurrent unsubscribe
	// racing this loop's send would otherwise panic. cn.stop is what ends
	// the loop when the connection goes away.
	for {
		select {
		case e := <-ch:
			cn.send(e)
		case <-cn.stop:
			return
		}
	}
}

func handleConn(nc net.Conn, threads map[string]*thread) {
	defer nc.Close()
	cn := &conn{c: nc, out: make(chan []byte, 4096), stop: make(chan struct{}), threads: threads, subChans: map[string]chan proto.Event{}}
	go cn.writer()

	defer func() {
		close(cn.stop)
		cn.wg.Wait()
		cn.subChansM.Lock()
		for name, ch := range cn.subChans {
			threads[name].unsubscribe(ch)
		}
		cn.subChansM.Unlock()
		close(cn.out)
	}()

	scanner := bufio.NewScanner(nc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var c proto.Command
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			continue
		}
		switch c.Cmd {
		case "list":
			names := make([]string, 0, len(threads))
			for n := range threads {
				names = append(names, n)
			}
			cn.send(proto.ListMsg{Type: "list", Threads: names})
		case "subscribe":
			t, ok := threads[c.Thread]
			if !ok {
				continue
			}
			cn.subChansM.Lock()
			_, already := cn.subChans[c.Thread]
			cn.subChansM.Unlock()
			if !already {
				cn.wg.Add(1)
				go cn.forward(c.Thread, t)
			}
		case "answer":
			if t, ok := threads[c.Thread]; ok {
				t.answer(c.Value)
			}
		case "send":
			if t, ok := threads[c.Thread]; ok {
				t.emit(proto.KindAgent, "user: "+c.Text)
			}
		}
	}
}

func main() {
	sockPath := filepath.Join(os.TempDir(), "wavezd.sock")
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	log.Printf("wavezd listening on %s", sockPath)

	threads := map[string]*thread{"a": newThread("a"), "b": newThread("b"), "c": newThread("c")}
	stop := make(chan struct{})
	for _, t := range threads {
		go t.run(stop)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
		ln.Close()
		_ = os.Remove(sockPath)
		os.Exit(0)
	}()

	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go handleConn(nc, threads)
	}
}
