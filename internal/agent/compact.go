package agent

import (
	"fmt"
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
)

// DefaultCompactTrigger is the share of the routed model's context budget a
// request may reach before Run compacts. Below one, so compaction happens
// while there is still room to send the compacted request.
const DefaultCompactTrigger = 0.75

// DefaultCacheLifetime is how long a provider's prompt cache is assumed to
// survive between requests. Past it the whole prefix is re-read at full
// price, so a run coming back from a longer wait compacts before it asks
// again: the cache is already gone, and a smaller prefix is what it pays for.
//
// Measured over this project's thread logs, across 421 turns: every request
// that hit the cache followed a gap of 352 seconds or less, and every request
// that missed it followed a gap of 736 seconds or more. Ten minutes sits
// inside the cold side of that gap, because compacting while the cache is
// still warm invalidates the part of the prefix it rewrites and costs rather
// than saves. Seven cold turns in that sample re-read 510,696 input tokens.
const DefaultCacheLifetime = 10 * time.Minute

// ColdCompactTrigger is the share of the context budget a request must reach
// before a run that lost the prompt cache compacts. Compaction appends to a
// prefix that is never rewritten, so it cannot be undone: a resume on a small
// history would mask that history permanently to save a fraction of a window
// that was never close to full.
const ColdCompactTrigger = 0.25

// DefaultObservationShare is the share of the routed tier's context budget
// that tool output may occupy before the oldest of it is masked. Sizing
// retention against the served window is what keeps one rule right for a 12k
// local window and a hosted one in the hundreds of thousands.
const DefaultObservationShare = 0.4

// WithCompaction configures Run to compact its history once an estimated
// request crosses trigger of the local context budget. Trigger is a share
// of that budget; zero leaves DefaultCompactTrigger.
func WithCompaction(opts thread.CompactOptions, trigger float64) Option {
	return func(o *Options) {
		o.Compact = opts
		o.CompactEnabled = true

		if trigger > 0 {
			o.CompactTrigger = trigger
		}
	}
}

// WithCacheLifetime sets how long the provider's prompt cache is assumed to
// last between requests; zero leaves DefaultCacheLifetime.
func WithCacheLifetime(d time.Duration) Option {
	return func(o *Options) {
		if d > 0 {
			o.CacheLifetime = d
		}
	}
}

// messages returns the history for the next request: the compacted prefix
// verbatim, then every entry appended since it was taken, with a tool result
// that byte-matches an earlier one replaced by a reference to it.
//
// The dedupe runs on every request rather than only when compaction fires,
// because compaction fires on crossing a share of the routed tier's budget
// and a hosted turn never crosses it, while the duplicate is re-sent on every
// turn until the run ends. Measured over this project's thread logs, 94 of
// 281 reads re-read a path the thread had already read with no edit in
// between, 36.5% of all read bytes.
//
// It is safe to apply at the edge because it keeps the first copy and only
// rewrites later ones, so a message the model has seen never changes and the
// provider's cached prefix stays put: a result deduped on one turn is
// deduped identically on the next.
func (r *run) messages() []llm.Message {
	full := r.thread.TurnHistory()

	out := full
	if r.compactedThrough > 0 {
		out = make([]thread.TurnMessage, 0, len(r.compacted)+len(full)-r.compactedThrough)
		out = append(out, r.compacted...)
		out = append(out, full[r.compactedThrough:]...)
	}

	deduped, _ := thread.DedupeToolReads(out)

	return thread.Flatten(deduped)
}

// compactBudget is the served window of the tier this turn would route to
// at its current size, which is what compaction has to be measured against.
// Sizing it from the fast tier's window whatever the turn was routed to
// compacted a hosted run at 6k of a window in the hundreds of thousands: 15
// compactions across 23 turns, every tool result older than four turns
// replaced by a reference, and the model read its way back to what it had
// (14 of 19 reads were repeats) until the run hit its deadline having made
// no edit at all.
func (r *run) compactBudget(estimated int) int {
	choice := router.Route(r.routeInput(estimated)).Choice

	return router.ContextBudget(choice, r.loop.ContextWindow())
}

// maybeCompact compacts the history when the next request would cross the
// configured share of the context budget, or when the run has just come back
// from a wait long enough to have lost the provider's prompt cache. It does
// nothing otherwise.
//
// It compacts only the entries appended since the last compaction and
// appends the result to the prefix already taken, so no message the model
// has seen ever changes. Recompacting the whole history would edit the
// middle of the provider's cached prefix, which measured 5-7x the cost of
// an append on the local runtime. Thread's own entries are never touched
// either, so the event log keeps every turn the compacted view drops.
func (r *run) maybeCompact(estimated int) error {
	if !r.loop.options.CompactEnabled {
		return nil
	}

	cold := r.prefixCold
	r.prefixCold = false

	trigger := r.loop.options.CompactTrigger
	if cold {
		trigger = ColdCompactTrigger
	}

	if float64(estimated) < trigger*float64(r.compactBudget(estimated)) {
		return nil
	}

	full := r.thread.TurnHistory()
	if len(full) <= r.compactedThrough {
		return nil
	}

	opts := r.loop.options.Compact
	opts.ObservationBudget = int(DefaultObservationShare * float64(r.compactBudget(estimated)))

	fresh, report := thread.Compact(full[r.compactedThrough:], opts)
	if report.TotalTokens <= 0 {
		return nil
	}

	r.compacted = append(r.compacted, fresh...)
	r.compactedThrough = len(full)
	r.outcome.TokensCompacted += report.TotalTokens

	ev := event.Event{
		Kind:   event.KindUsage,
		Text:   fmt.Sprintf("compacted history, saving ~%d tokens", report.TotalTokens),
		Detail: map[string]any{"tokens_saved": report.TotalTokens, "rules": report.Rules},
	}
	if _, err := r.thread.Log().Append(ev); err != nil {
		return fmt.Errorf("logging compaction: %w", err)
	}

	return nil
}
