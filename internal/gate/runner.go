package gate

import (
	"context"
	"sort"
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// RunFunc executes one debounced batch of coalesced changes and returns its
// result. Selection and gate execution live in the RunFunc a caller builds
// (see BuildRunFunc), not in Runner itself, so Runner stays only the
// debounce and coalesce mechanism DESIGN.md's Gates section calls for.
type RunFunc func(ctx context.Context, changes []tool.Change) RunResult

// RunResult is what one debounced batch produced.
type RunResult struct {
	LogError error
	Changes  []tool.Change
	Gates    []Result
}

// Runner debounces and coalesces tool.Change events from an edit tool or a
// file watcher into single gate runs, per DESIGN.md's Gates section: gates
// trigger on change events, never on the model deciding to test. A Runner
// is only useful after Start runs in its own goroutine.
type Runner struct {
	clock    Clock
	run      RunFunc
	changeCh chan changeMsg
	results  chan RunResult
	debounce time.Duration
}

// changeMsg pairs a change with an acknowledgement Start closes only once
// the change is fully coalesced and the debounce timer is (re)armed, so
// Enqueue can hand a caller (a test, in particular) a synchronous
// guarantee that the state before the ack is visible before it returns.
type changeMsg struct {
	done   chan struct{}
	change tool.Change
}

// NewRunner builds a Runner that waits debounce after the last change in a
// burst before invoking run with the coalesced batch.
func NewRunner(clock Clock, debounce time.Duration, run RunFunc) *Runner {
	return &Runner{
		clock:    clock,
		debounce: debounce,
		run:      run,
		changeCh: make(chan changeMsg),
		results:  make(chan RunResult, 1),
	}
}

// Enqueue records one tool.Change, coalescing it into the pending batch and
// restarting the debounce window. It returns only once Start has finished
// applying the change, which is what makes a burst of Enqueue calls
// deterministic in tests without any sleep.
func (r *Runner) Enqueue(c tool.Change) {
	done := make(chan struct{})
	r.changeCh <- changeMsg{change: c, done: done}
	<-done
}

// Results streams one RunResult per debounced batch.
func (r *Runner) Results() <-chan RunResult {
	return r.results
}

// Start runs the debounce loop until ctx is done. Callers run it in its own
// goroutine.
func (r *Runner) Start(ctx context.Context) {
	// One batch per writer: one agent.Loop serves every thread, so a single
	// pending map coalesced two lanes' work into one run and handed each of
	// them the other's findings as its own.
	pending := make(map[string]map[string]tool.Change)

	var timer Timer

	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}

			return
		case msg := <-r.changeCh:
			byPath, ok := pending[msg.change.Writer]
			if !ok {
				byPath = make(map[string]tool.Change)
				pending[msg.change.Writer] = byPath
			}

			coalesce(byPath, msg.change)

			if timer == nil {
				timer = r.clock.NewTimer(r.debounce)
				timerC = timer.C()
			} else {
				timer.Stop()
				timer.Reset(r.debounce)
			}

			close(msg.done)
		case <-timerC:
			for _, byPath := range pending {
				r.results <- r.run(ctx, drain(byPath))
			}

			pending = make(map[string]map[string]tool.Change)
			timer = nil
			timerC = nil
		}
	}
}

func coalesce(pending map[string]tool.Change, c tool.Change) {
	existing, ok := pending[c.Path]
	if !ok {
		pending[c.Path] = c

		return
	}

	existing.Added += c.Added
	existing.Removed += c.Removed
	existing.Ranges = mergeRanges(existing.Ranges, c.Ranges)
	pending[c.Path] = existing
}

func mergeRanges(a, b []tool.LineRange) []tool.LineRange {
	out := append([]tool.LineRange(nil), a...)

	for _, r := range b {
		dup := false

		for _, e := range out {
			if e == r {
				dup = true

				break
			}
		}

		if !dup {
			out = append(out, r)
		}
	}

	return out
}

func drain(pending map[string]tool.Change) []tool.Change {
	out := make([]tool.Change, 0, len(pending))
	for _, c := range pending {
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	return out
}
