package replay

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/router"
)

// Column widths of the run table, wide enough for a commit hash, a tier
// name, and the longest stop reason.
const (
	labelWidth = 20
	modelWidth = 10
	stopWidth  = 12
	tiersWidth = 12
)

// pairSize is how many runs a diff needs.
const pairSize = 2

// Report writes one row per recorded run of task, oldest last, then diffs
// the two most recent counter by counter. Two runs of the same task are what
// a lane is judged on, so the diff is the point and the rows are the context
// for it: three runs of one lane that disagree say the pair proved nothing.
func Report(recs []Record, task string, w io.Writer) error {
	rows := ForTask(recs, task)
	if len(rows) == 0 {
		if _, err := fmt.Fprintf(w, "no runs recorded for task %s\n", task); err != nil {
			return fmt.Errorf("writing replay report: %w", err)
		}

		return nil
	}

	var b strings.Builder

	fmt.Fprintf(&b, "task %s, %d run(s)\n", task, len(rows))
	fmt.Fprintf(&b, "%-20s %-20s %-10s %-14s %-12s %6s %6s %6s %10s %8s\n",
		"label", "started", "asked", "tiers", "stop", "checks", "turns", "calls", "in tokens", "elapsed")

	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(&b, "%-20s %-20s %-10s %-14s %-12s %6s %6d %6d %10d %8s\n",
			truncate(r.Label, labelWidth), r.Started, truncate(modelOf(r.Run), modelWidth),
			truncate(TierMix(r.Stats.TierTurns), tiersWidth), truncate(r.Stop, stopWidth),
			r.CheckSummary(), r.Stats.Turns, r.Stats.ToolCalls,
			r.Stats.InputTokens, r.Stats.Elapsed.Round(time.Second))
	}

	for i := range rows {
		if note := escalationNote(&rows[i]); note != "" {
			b.WriteString(note)
		}
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing replay report: %w", err)
	}

	if len(rows) < pairSize {
		return nil
	}

	last := rows[len(rows)-1]

	prev, ok := baselineFor(rows)
	if !ok {
		return nil
	}

	if _, err := fmt.Fprintf(w, "\n%s -> %s\n", prev.Label, last.Label); err != nil {
		return fmt.Errorf("writing replay report: %w", err)
	}

	if prev.TaskHash != last.TaskHash {
		_, err := fmt.Fprintf(w, "the task text changed between these runs (%s then %s), "+
			"so they answered different questions\n", prev.TaskHash, last.TaskHash)
		if err != nil {
			return fmt.Errorf("writing replay report: %w", err)
		}
	}

	if !prev.SameSetup(last.Run) {
		_, err := fmt.Fprintf(w,
			"setup differs (model %s vs %s, max-turns %d vs %d%s), so this diff mixes the lane with the setup\n",
			modelOf(prev.Run), modelOf(last.Run), prev.MaxTurns, last.MaxTurns,
			servedDiff(prev.Served, last.Served))
		if err != nil {
			return fmt.Errorf("writing replay report: %w", err)
		}
	}

	return bench.Compare(prev.Stats, last.Stats, w) //nolint:wrapcheck // Compare's error already names the writer
}

// baselineFor is the run the newest one is diffed against: the most recent
// earlier run that actually took a turn. A run that died before its first
// turn carries zeros, and diffing against zeros reports the whole run as the
// improvement.
func baselineFor(rows []Record) (Record, bool) {
	for i := len(rows) - pairSize; i >= 0; i-- {
		if rows[i].Stats.Turns > 0 {
			return rows[i], true
		}
	}

	return Record{}, false
}

// TierMix renders the turns a run spent on each tier, weakest first and
// zeroes omitted ("6f 12b"). A pin is a floor rather than a cage, so the
// tier a run asked for is not the tier that did the work, and reading the
// pin as the answer credits the fast tier with a hosted model's run.
func TierMix(turns map[string]int) string {
	var parts []string

	for _, tier := range []router.Choice{router.ChoiceFast, router.ChoiceBalanced, router.ChoiceDeep} {
		if n := turns[string(tier)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d%c", n, tier[0]))
		}
	}

	if len(parts) == 0 {
		return "-"
	}

	return strings.Join(parts, " ")
}

// escalationNote calls out a run that finished above the tier it asked for.
// The row already carries the mix, and the mix is easy to read past, so the
// case the fast tier is judged on gets its own line: what the pinned tier
// spent before it gave up, and who finished.
func escalationNote(r *Record) string {
	pinned := r.Model
	if pinned == "" || r.Stats.Turns == 0 {
		return ""
	}

	own := r.Stats.TierTurns[pinned]
	if own == r.Stats.Turns {
		return ""
	}

	return fmt.Sprintf("  %s asked for %s and spent %d of %d turns above it\n",
		r.Label, pinned, r.Stats.Turns-own, r.Stats.Turns)
}

// modelOf names the tier a run was pinned to, or the router's own choice.
func modelOf(r Run) string {
	if r.Model == "" {
		return "routed"
	}

	return r.Model
}

func truncate(s string, width int) string {
	if len(s) <= width {
		return s
	}

	return s[:width-1] + "…"
}

// servedDiff names each tier that a different model or endpoint answered,
// which is the difference a tier name cannot show.
func servedDiff(prev, last map[string]string) string {
	var moved []string

	for tier, was := range prev {
		if now, ok := last[tier]; ok && now != was {
			moved = append(moved, fmt.Sprintf("%s served by %s vs %s", tier, was, now))
		}
	}

	if len(moved) == 0 {
		return ""
	}

	sort.Strings(moved)

	return ", " + strings.Join(moved, ", ")
}
