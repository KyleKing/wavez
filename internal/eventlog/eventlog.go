// Package eventlog stores a thread's append-only event stream and fans it out
// to subscribers. Memory is bounded by a ring; the JSONL file on disk is the
// complete record and is what a late or lagging subscriber reads from.
package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/event"
)

// ErrClosed reports use of a Log after Close.
var ErrClosed = errors.New("event log closed")

const (
	defaultRing        = 4096
	defaultQueue       = 4096
	defaultSubscribers = 8
	maxLineBytes       = 8 << 20
	initialLineBytes   = 64 << 10
	dirPerm            = 0o700
	filePerm           = 0o600
)

// Options tunes retention and fan-out.
type Options struct {
	// Ring caps how many recent events stay in memory. Older events are served
	// from disk.
	Ring int
	// Queue caps a subscriber's pending events before it is declared lagged.
	Queue int
}

// Log is one thread's event stream. It is safe for concurrent use.
type Log struct {
	file *os.File
	w    *bufio.Writer
	subs map[*subscriber]struct{}

	threadID string
	path     string
	ring     []event.Event

	mu       sync.RWMutex
	seq      uint64
	start    int
	count    int
	ringCap  int
	queueCap int
	closed   bool
}

// Open opens or creates the log for threadID under dir, resuming the sequence
// from the existing file so restarts do not reuse a Seq.
func Open(dir, threadID string, opt Options) (*Log, error) {
	if opt.Ring <= 0 {
		opt.Ring = defaultRing
	}
	if opt.Queue <= 0 {
		opt.Queue = defaultQueue
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}
	path := filepath.Join(dir, threadID+".jsonl")

	l := &Log{
		threadID: threadID,
		path:     path,
		ring:     make([]event.Event, opt.Ring),
		subs:     make(map[*subscriber]struct{}, defaultSubscribers),
		ringCap:  opt.Ring,
		queueCap: opt.Queue,
	}
	if err := l.replay(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm) //nolint:gosec // caller-owned log dir
	if err != nil {
		return nil, fmt.Errorf("opening log: %w", err)
	}
	l.file, l.w = f, bufio.NewWriter(f)

	return l, nil
}

func (l *Log) replay() error {
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only handle

	for ev, err := range decode(f) {
		if err != nil {
			return err
		}
		l.seq = ev.Seq
		l.push(ev)
	}

	return nil
}

// Append assigns the next Seq, persists the event, and delivers it to
// subscribers. The returned Seq is stable across restarts.
func (l *Log) Append(ev event.Event) (uint64, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()

		return 0, ErrClosed
	}
	l.seq++
	ev.Seq = l.seq
	ev.ThreadID = l.threadID
	if ev.At.IsZero() {
		ev.At = time.Now()
	}

	line, err := json.Marshal(ev)
	if err != nil {
		l.seq--
		l.mu.Unlock()

		return 0, fmt.Errorf("encoding event: %w", err)
	}
	if _, err := l.w.Write(append(line, '\n')); err != nil {
		l.seq--
		l.mu.Unlock()

		return 0, fmt.Errorf("writing event: %w", err)
	}
	if err := l.w.Flush(); err != nil {
		l.mu.Unlock()

		return 0, fmt.Errorf("flushing event: %w", err)
	}

	l.push(ev)
	subs := make([]*subscriber, 0, len(l.subs))
	for s := range l.subs {
		subs = append(subs, s)
	}
	l.mu.Unlock()

	for _, s := range subs {
		s.offer(ev)
	}

	return ev.Seq, nil
}

func (l *Log) push(ev event.Event) {
	idx := (l.start + l.count) % l.ringCap
	l.ring[idx] = ev
	if l.count == l.ringCap {
		l.start = (l.start + 1) % l.ringCap
	} else {
		l.count++
	}
}

// Head returns the highest assigned Seq.
func (l *Log) Head() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.seq
}

// Since yields every event with Seq > from, in order. Events still in the ring
// are served from memory; anything older is read from disk, so a caller that
// fell behind always converges.
func (l *Log) Since(from uint64) ([]event.Event, error) {
	l.mu.RLock()
	oldest := uint64(0)
	if l.count > 0 {
		oldest = l.ring[l.start].Seq
	}
	inMemory := l.count > 0 && from+1 >= oldest
	var out []event.Event
	if inMemory {
		for i := range l.count {
			ev := l.ring[(l.start+i)%l.ringCap]
			if ev.Seq > from {
				out = append(out, ev)
			}
		}
	}
	l.mu.RUnlock()
	if inMemory {
		return out, nil
	}

	return l.readSince(from)
}

func (l *Log) readSince(from uint64) ([]event.Event, error) {
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading log: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only handle

	var out []event.Event
	for ev, err := range decode(f) {
		if err != nil {
			return nil, err
		}
		if ev.Seq > from {
			out = append(out, ev)
		}
	}

	return out, nil
}

// Close flushes and releases the file. Subscribers stop on their own contexts.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	flushErr := l.w.Flush()
	closeErr := l.file.Close()
	if flushErr != nil {
		return fmt.Errorf("flushing log: %w", flushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing log: %w", closeErr)
	}

	return nil
}

func decode(r io.Reader) func(func(event.Event, error) bool) {
	return func(yield func(event.Event, error) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, initialLineBytes), maxLineBytes)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var ev event.Event
			if err := json.Unmarshal(line, &ev); err != nil {
				yield(event.Event{}, fmt.Errorf("decoding event: %w", err))

				return
			}
			if !yield(ev, nil) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			yield(event.Event{}, fmt.Errorf("scanning log: %w", err))
		}
	}
}
