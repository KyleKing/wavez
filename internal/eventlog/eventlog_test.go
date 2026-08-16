package eventlog_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/eventlog"
)

func open(t *testing.T, opt eventlog.Options) *eventlog.Log {
	t.Helper()
	l, err := eventlog.Open(t.TempDir(), "t1", opt)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return l
}

func appendN(t *testing.T, l *eventlog.Log, n uint64) {
	t.Helper()
	for i := range n {
		if _, err := l.Append(event.Event{Kind: event.KindAgent, Text: strconv.FormatUint(i, 10)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

func TestSeqAndSince(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ring     int
		appended uint64
		from     uint64
		want     uint64
	}{
		{"all from memory", 64, 10, 0, 10},
		{"tail from memory", 64, 10, 7, 3},
		{"past head", 64, 10, 10, 0},
		{"spilled to disk", 4, 20, 0, 20},
		{"spilled tail", 4, 20, 15, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := open(t, eventlog.Options{Ring: tc.ring})
			appendN(t, l, tc.appended)
			if got := l.Head(); got != tc.appended {
				t.Fatalf("head = %d, want %d", got, tc.appended)
			}
			got, err := l.Since(tc.from)
			if err != nil {
				t.Fatalf("since: %v", err)
			}
			if uint64(len(got)) != tc.want {
				t.Fatalf("since(%d) returned %d events, want %d", tc.from, len(got), tc.want)
			}
			for i, ev := range got {
				if want := tc.from + uint64(i) + 1; ev.Seq != want {
					t.Fatalf("event %d has seq %d, want %d", i, ev.Seq, want)
				}
			}
		})
	}
}

func TestReopenResumesSeq(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := eventlog.Open(dir, "t1", eventlog.Options{Ring: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendN(t, first, 9)
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := eventlog.Open(dir, "t1", eventlog.Options{Ring: 4})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	seq, err := second.Append(event.Event{Kind: event.KindUser, Text: "after restart"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if seq != 10 {
		t.Fatalf("seq after reopen = %d, want 10", seq)
	}
	all, err := second.Since(0)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(all) != 10 {
		t.Fatalf("history after reopen has %d events, want 10", len(all))
	}
}

func TestAppendAfterCloseFails(t *testing.T) {
	t.Parallel()
	l := open(t, eventlog.Options{})
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := l.Append(event.Event{Kind: event.KindAgent}); err == nil {
		t.Fatal("append after close succeeded, want ErrClosed")
	}
}

// A fresh subscriber must replay the entire backlog. The daemon spike lost
// 2700 of 4700 events here by dropping on a full outbound buffer.
func TestSubscribeReplaysFullBacklog(t *testing.T) {
	t.Parallel()
	const total = 5000
	l := open(t, eventlog.Options{Ring: 128, Queue: 8})
	appendN(t, l, total)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	updates, err := l.Subscribe(ctx, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	var seen uint64
	for u := range updates {
		if u.Lagged {
			t.Fatal("backlog replay reported a gap, backlog must never be dropped")
		}
		seen++
		if u.Event.Seq != seen {
			t.Fatalf("event %d out of order: seq %d", seen, u.Event.Seq)
		}
		if seen == total {
			break
		}
	}
	if seen != total {
		t.Fatalf("replayed %d of %d events", seen, total)
	}
}

func TestSubscribeDeliversLiveEvents(t *testing.T) {
	t.Parallel()
	l := open(t, eventlog.Options{})
	updates, err := l.Subscribe(t.Context(), 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := l.Append(event.Event{Kind: event.KindUser, Text: "hello"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	select {
	case u := <-updates:
		if u.Event.Text != "hello" || u.Event.Seq != 1 {
			t.Fatalf("got %+v", u.Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live event never arrived")
	}
}

// A subscriber that stops reading must be told it has a gap rather than
// silently losing events or blocking the producer.
func TestSlowSubscriberReportsLagInsteadOfLosingEvents(t *testing.T) {
	t.Parallel()
	l := open(t, eventlog.Options{Ring: 8, Queue: 2})
	updates, err := l.Subscribe(t.Context(), 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	appendN(t, l, 200)

	deadline := time.After(5 * time.Second)
	var lagged bool
	var last uint64
	for !lagged {
		select {
		case u := <-updates:
			if u.Lagged {
				lagged, last = true, u.LastSeq
				continue
			}
			last = u.Event.Seq
		case <-deadline:
			t.Fatal("producer stalled or lag was never reported")
		}
	}
	rest, err := l.Since(last)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if last+uint64(len(rest)) != 200 {
		t.Fatalf("resync from %d yielded %d events, want to reach 200", last, len(rest))
	}
}

// The spike panicked with "send on closed channel" when a subscriber went away
// while the producer was mid-fanout.
func TestConcurrentSubscribeAndAppendIsRaceFree(t *testing.T) {
	t.Parallel()

	const (
		appends = 20_000
		cycles  = 400
	)
	l := open(t, eventlog.Options{Ring: 64, Queue: 4})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range uint64(appends) {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := l.Append(event.Event{Kind: event.KindAgent, Text: strconv.FormatUint(i, 10)}); err != nil {
				return
			}
		}
	}()

	for range cycles {
		ctx, cancel := context.WithCancel(t.Context())
		updates, err := l.Subscribe(ctx, l.Head())
		if err != nil {
			cancel()
			t.Fatalf("subscribe: %v", err)
		}
		select {
		case <-updates:
		default:
		}
		cancel()
		for range updates { //nolint:revive // drain until the forwarder exits
		}
	}
	close(stop)
	wg.Wait()
}
