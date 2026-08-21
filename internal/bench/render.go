package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Render writes a human-readable record of one run. Every line is a number a
// later run can be compared against, so nothing here is prose.
func (s Stats) Render(w io.Writer) error {
	var b strings.Builder

	fmt.Fprintf(&b, "thread %s\n", s.ThreadID)
	fmt.Fprintf(&b, "turns %d, tool calls %d, elapsed %s\n",
		s.Turns, s.ToolCalls, s.Elapsed.Round(time.Second))
	fmt.Fprintf(&b, "tokens in %d, out %d, cache read %d (%s of input)\n",
		s.InputTokens, s.OutputTokens, s.CacheReadTokens, percent(s.CacheReadTokens, s.InputTokens))

	if len(s.TierTurns) > 0 {
		fmt.Fprintf(&b, "tiers %s\n", tierLine(s.TierTurns))
	}

	b.WriteString("\ntool calls by name\n")

	for _, t := range s.Tools {
		fmt.Fprintf(&b, "  %-14s %3d calls %8d result bytes\n", t.Name, t.Calls, t.ResultBytes)
	}

	fmt.Fprintf(&b, "\nrepeat reads %d of %d (%d bytes), empty searches %d\n",
		s.RepeatReads, callsOf(s.Tools, readTool), s.RepeatReadBytes, s.EmptySearches)
	fmt.Fprintf(&b, "gate rounds %d, failed %d, review objections %d, compaction saved ~%d tokens\n",
		s.GateRounds, s.GateFailures, s.ReviewObjections, s.CompactionSaved)

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing stats: %w", err)
	}

	return nil
}

// RenderJSON writes the same fields Render does as a single JSON object, so a
// script can diff one run against another. Elapsed is whole nanoseconds, the
// form a script can subtract without parsing Render's rounded duration.
func (s Stats) RenderJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("encoding stats: %w", err)
	}

	return nil
}

func callsOf(tools []ToolStat, name string) int {
	for _, t := range tools {
		if t.Name == name {
			return t.Calls
		}
	}

	return 0
}

func percent(part, whole int) string {
	if whole == 0 {
		return "n/a"
	}

	const full = 100

	return fmt.Sprintf("%d%%", part*full/whole)
}

func tierLine(turns map[string]int) string {
	var parts []string
	for _, tier := range []string{"fast", "balanced", "deep"} {
		if n, ok := turns[tier]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", tier, n))
		}
	}

	return strings.Join(parts, ", ")
}
