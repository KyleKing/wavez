package bench

import (
	"fmt"
	"io"
	"strings"
)

// Compare writes one line per numeric field a run is judged by, showing the
// baseline value, the current value, and the signed change between them. Only
// the counters take part: the per-tool breakdown and the tier split have no
// stable rows across two runs, so they stay in Render and RenderJSON, and a
// script can diff this output line for line.
func Compare(baseline, current Stats, w io.Writer) error {
	rows := []struct {
		label   string
		before  int
		current int
	}{
		{"turns", baseline.Turns, current.Turns},
		{"tool calls", baseline.ToolCalls, current.ToolCalls},
		{"input tokens", baseline.InputTokens, current.InputTokens},
		{"output tokens", baseline.OutputTokens, current.OutputTokens},
		{"cache read tokens", baseline.CacheReadTokens, current.CacheReadTokens},
		{"repeat reads", baseline.RepeatReads, current.RepeatReads},
		{"repeat read bytes", baseline.RepeatReadBytes, current.RepeatReadBytes},
		{"empty searches", baseline.EmptySearches, current.EmptySearches},
		{"gate rounds", baseline.GateRounds, current.GateRounds},
		{"gate failures", baseline.GateFailures, current.GateFailures},
		{"review objections", baseline.ReviewObjections, current.ReviewObjections},
		{"compaction saved", baseline.CompactionSaved, current.CompactionSaved},
	}

	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-20s %10d %10d %+d\n", r.label, r.before, r.current, r.current-r.before)
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing stats comparison: %w", err)
	}

	return nil
}
