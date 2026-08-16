package eventlog

import (
	"context"
	"sync"

	"github.com/kyleking/wavez/internal/event"
)

// Update is one delivery to a subscriber. Lagged reports a gap: the subscriber
// fell far enough behind that its queue was dropped, and it must resync with
// Since(seq of the last Event it received). LastSeq is the log head at that
// moment, so a consumer can tell how far behind it fell.
type Update struct {
	Event   event.Event
	LastSeq uint64
	Lagged  bool
}

type batch struct {
	events  []event.Event
	lastSeq uint64
	lagged  bool
}

type subscriber struct {
	notify chan struct{}
	queue  []event.Event

	mu      sync.Mutex
	cap     int
	lastSeq uint64
	lagged  bool
}

func (s *subscriber) offer(ev event.Event) {
	s.mu.Lock()
	if len(s.queue) >= s.cap {
		s.queue = s.queue[:0]
		s.lagged = true
	}
	s.queue = append(s.queue, ev)
	s.lastSeq = ev.Seq
	s.mu.Unlock()

	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *subscriber) drain() batch {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := batch{events: s.queue, lastSeq: s.lastSeq, lagged: s.lagged}
	s.queue, s.lagged = nil, false
	if b.lagged {
		b.events = nil
	}

	return b
}

// Subscribe delivers every event with Seq > from, backlog first and then live.
// Backlog is never dropped. A subscriber that cannot keep up with the live
// stream receives an Update with Lagged set instead of a silent gap. The
// returned channel is closed when ctx is done or the log is closed.
func (l *Log) Subscribe(ctx context.Context, from uint64) (<-chan Update, error) {
	sub := &subscriber{cap: l.queueCap, notify: make(chan struct{}, 1)}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()

		return nil, ErrClosed
	}
	head := l.seq
	l.subs[sub] = struct{}{}
	l.mu.Unlock()

	backlog, err := l.Since(from)
	if err != nil {
		l.unsubscribe(sub)

		return nil, err
	}

	out := make(chan Update, 1)
	go l.forward(ctx, sub, out, backlog, head)

	return out, nil
}

func (l *Log) forward(ctx context.Context, sub *subscriber, out chan Update, backlog []event.Event, head uint64) {
	defer close(out)
	defer l.unsubscribe(sub)

	for i := range backlog {
		if backlog[i].Seq > head {
			continue
		}
		if !send(ctx, out, Update{Event: backlog[i]}) {
			return
		}
	}
	for {
		b := sub.drain()
		if b.lagged && !send(ctx, out, Update{Lagged: true, LastSeq: b.lastSeq}) {
			return
		}
		for i := range b.events {
			if b.events[i].Seq <= head {
				continue
			}
			if !send(ctx, out, Update{Event: b.events[i]}) {
				return
			}
		}
		select {
		case <-sub.notify:
		case <-ctx.Done():
			return
		}
	}
}

func send(ctx context.Context, out chan Update, u Update) bool {
	select {
	case out <- u:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *Log) unsubscribe(sub *subscriber) {
	l.mu.Lock()
	delete(l.subs, sub)
	l.mu.Unlock()
}
