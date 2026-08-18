package thread

import (
	"context"
	"fmt"
	"sort"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// LedgerSummary is the structural facts a session ledger line derives from a
// thread's event log: no model call in v0.1.
type LedgerSummary struct {
	FilesChanged []string
	Turns        int
	GatesRun     int
}

// Ledger derives a LedgerSummary from events. Turns counts KindAgent events
// (one per completed model response), FilesChanged is the sorted, deduplicated
// set of paths any KindTool event's Changes named, and GatesRun counts
// KindGate events.
func Ledger(events []event.Event) LedgerSummary {
	files := make(map[string]struct{})

	var summary LedgerSummary

	for i := range events {
		ev := &events[i]

		switch ev.Kind {
		case event.KindAgent:
			summary.Turns++
		case event.KindGate:
			summary.GatesRun++
		case event.KindTool:
			for _, ch := range ev.Changes {
				files[ch.Path] = struct{}{}
			}
		case event.KindUser, event.KindPermission, event.KindState, event.KindError, event.KindLedger,
			event.KindUsage, event.KindReview, event.KindCycle, event.KindHypothesis:
		}
	}

	summary.FilesChanged = make([]string, 0, len(files))
	for f := range files {
		summary.FilesChanged = append(summary.FilesChanged, f)
	}
	sort.Strings(summary.FilesChanged)

	return summary
}

// WriteLedger computes a LedgerSummary from the thread's full event log and
// appends it as a KindLedger event, the one line a session ledger records at
// thread end.
func (t *Thread) WriteLedger(ctx context.Context) (LedgerSummary, error) {
	if err := contextErr(ctx); err != nil {
		return LedgerSummary{}, err
	}

	events, err := t.log.Since(0)
	if err != nil {
		return LedgerSummary{}, fmt.Errorf("reading thread log: %w", err)
	}

	summary := Ledger(events)
	text := fmt.Sprintf(
		"%d turns, %d files changed, %d gates run",
		summary.Turns, len(summary.FilesChanged), summary.GatesRun,
	)
	detail := map[string]any{
		"files_changed": summary.FilesChanged,
		"gates_run":     summary.GatesRun,
		"turns":         summary.Turns,
	}

	if _, err := t.log.Append(event.Event{Kind: event.KindLedger, Text: text, Detail: detail}); err != nil {
		return LedgerSummary{}, fmt.Errorf("logging ledger: %w", err)
	}

	return summary, nil
}

// ChangeSet collapses a thread's events to one entry per changed file,
// keeping the last line ranges recorded for it. It is what a thread's work
// amounts to once its prose is dropped, which is what a fork inherits and
// what a Cycle phase hands to the next.
func ChangeSet(events []event.Event) []tool.Change {
	byPath := map[string]tool.Change{}

	var order []string

	for i := range events {
		for _, c := range events[i].Changes {
			if _, seen := byPath[c.Path]; !seen {
				order = append(order, c.Path)
			}

			byPath[c.Path] = c
		}
	}

	out := make([]tool.Change, 0, len(order))
	for _, path := range order {
		out = append(out, byPath[path])
	}

	return out
}
