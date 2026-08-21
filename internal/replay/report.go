package replay

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/bench"
)

// Column widths of the run table, wide enough for a commit hash, a tier
// name, and the longest stop reason.
const (
	labelWidth = 20
	modelWidth = 10
	stopWidth  = 12
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
	fmt.Fprintf(&b, "%-20s %-20s %-10s %-12s %6s %6s %10s %8s\n",
		"label", "started", "model", "stop", "turns", "calls", "in tokens", "elapsed")

	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(&b, "%-20s %-20s %-10s %-12s %6d %6d %10d %8s\n",
			truncate(r.Label, labelWidth), r.Started, truncate(modelOf(r.Run), modelWidth),
			truncate(r.Stop, stopWidth), r.Stats.Turns, r.Stats.ToolCalls, r.Stats.InputTokens,
			r.Stats.Elapsed.Round(time.Second))
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

	if !prev.SameSetup(last.Run) {
		_, err := fmt.Fprintf(w,
			"setup differs (model %s vs %s, max-turns %d vs %d), so this diff mixes the lane with the setup\n",
			modelOf(prev.Run), modelOf(last.Run), prev.MaxTurns, last.MaxTurns)
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
