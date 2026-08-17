package agent

import (
	"fmt"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
)

// DefaultCompactTrigger is the share of the routed model's context budget a
// request may reach before Run compacts. Below one, so compaction happens
// while there is still room to send the compacted request.
const DefaultCompactTrigger = 0.75

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

// messages returns the history for the next request: the compacted prefix
// verbatim, then every entry appended since it was taken.
func (r *run) messages() []llm.Message {
	if r.compactedThrough == 0 {
		return r.thread.History()
	}

	full := r.thread.TurnHistory()
	out := make([]thread.TurnMessage, 0, len(r.compacted)+len(full)-r.compactedThrough)
	out = append(out, r.compacted...)
	out = append(out, full[r.compactedThrough:]...)

	return thread.Flatten(out)
}

// maybeCompact compacts the history when the next request would cross the
// configured share of the context budget, and does nothing otherwise.
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

	if float64(estimated) < r.loop.options.CompactTrigger*float64(router.LocalContextBudget) {
		return nil
	}

	full := r.thread.TurnHistory()
	if len(full) <= r.compactedThrough {
		return nil
	}

	fresh, report := thread.Compact(full[r.compactedThrough:], r.thread.Turn(), r.loop.options.Compact)
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
