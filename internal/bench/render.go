package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
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
		fmt.Fprintf(&b, "  %-14s %3d calls %8d result bytes %d errors%s\n",
			t.Name, t.Calls, t.ResultBytes, t.Errors, causeLine(t))
	}

	if len(s.ShellCmds) > 0 {
		b.WriteString("\nshell commands by result size\n")

		cmds := slices.Clone(s.ShellCmds)
		slices.SortStableFunc(cmds, func(a, b ShellCmd) int { return b.ResultBytes - a.ResultBytes })

		if len(cmds) > maxShellCommands {
			cmds = cmds[:maxShellCommands]
		}

		for _, c := range cmds {
			fmt.Fprintf(&b, "  %8d bytes  %s\n", c.ResultBytes, oneLine(c.Command, maxCommandChars))
		}
	}

	fmt.Fprintf(&b, "\nrepeat reads %d of %d (%d bytes), empty searches %d, error results %d\n",
		s.RepeatReads, callsOf(s.Tools, readTool), s.RepeatReadBytes, s.EmptySearches, s.ErrorResults)
	fmt.Fprintf(&b, "gate rounds %d, failed %d, retracted %d, review objections %d, "+
		"compaction saved ~%d tokens\n",
		s.GateRounds, s.GateFailures, s.GateFalseAlarms, s.ReviewObjections, s.CompactionSaved)

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

// causeLine names why a tool's errors happened, most common first. It is
// appended rather than tabulated because a run whose tools all worked
// should print nothing extra, and because the count that matters is the
// share of a rate that was the tool refusing on purpose.
func causeLine(t ToolStat) string {
	if len(t.Causes) == 0 {
		return ""
	}

	causes := make([]string, 0, len(t.Causes))
	for cause := range t.Causes {
		causes = append(causes, cause)
	}

	slices.SortStableFunc(causes, func(a, b string) int {
		if n := t.Causes[b] - t.Causes[a]; n != 0 {
			return n
		}

		return strings.Compare(a, b)
	})

	parts := make([]string, 0, len(causes))
	for _, cause := range causes {
		parts = append(parts, fmt.Sprintf("%s %d", cause, t.Causes[cause]))
	}

	return " (" + strings.Join(parts, ", ") + ")"
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

const (
	// A run past this many commands is read for shape rather than inventory.
	maxShellCommands = 15
	// The most of one command a report row carries.
	maxCommandChars = 100
)

// oneLine flattens s onto one line and cuts it to max characters, since a
// command may hold newlines and a report row is one line.
func oneLine(s string, width int) string {
	s = strings.ReplaceAll(s, "\n", " ")

	runes := []rune(s)
	if len(runes) <= width {
		return s
	}

	return string(runes[:width-1]) + "…"
}
