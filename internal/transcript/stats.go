package transcript

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// Stats is the attribution of one session.
type Stats struct {
	// ToolCalls and ToolErrors count by tool name.
	ToolCalls  map[string]int `json:"tool_calls,omitempty"`
	ToolErrors map[string]int `json:"tool_errors,omitempty"`
	// SweepCommands counts untargeted sweeps by the command that ran.
	SweepCommands map[string]int `json:"sweep_commands,omitempty"`
	// Refactors counts `wavez rename|move|delete` calls by verb.
	Refactors map[string]int `json:"refactors,omitempty"`
	// Turns is the number of assistant messages, each one request to the
	// model, with subagent turns included.
	Turns int `json:"turns"`
	// Retrieval, Edit, Shell, Other, and Prose split Turns by the strongest
	// thing each turn did.
	Retrieval int `json:"retrieval_turns"`
	Edit      int `json:"edit_turns"`
	Shell     int `json:"shell_turns"`
	Other     int `json:"other_turns"`
	Prose     int `json:"prose_turns"`
	// RepeatReads counts reads of a file already read and not edited since.
	RepeatReads int `json:"repeat_reads"`
	// Sweeps counts whole-project build, test, and lint runs through the
	// shell: the number the gates are meant to drive to zero.
	Sweeps int `json:"sweeps"`
	// StopBlocks counts the times a Stop hook refused to let the session
	// end.
	StopBlocks int `json:"stop_blocks"`
	// TurnsAfterLastEdit counts the assistant turns that came after the
	// turn that last wrote a file: an edit tool, a shell command that
	// redirects into a file, or a name-based refactor. It is 0 for a
	// session that wrote nothing. These are the turns an agent spends
	// checking its own work and saying it is done, which is what a gate
	// that answers at the stop is meant to shorten. Subagent turns are not
	// in it: it is read off the main transcript alone.
	TurnsAfterLastEdit int `json:"turns_after_last_edit"`
	// Subagents is how many subagent transcripts were folded in.
	Subagents int `json:"subagents,omitempty"`
	// Skipped counts lines that were not valid JSON.
	Skipped int `json:"skipped_lines,omitempty"`
}

// TotalToolCalls is every tool call in the session.
func (s *Stats) TotalToolCalls() int { return sum(s.ToolCalls) }

// TotalToolErrors is every tool call whose result was an error.
func (s *Stats) TotalToolErrors() int { return sum(s.ToolErrors) }

// TotalRefactors is every call to a name-based refactor command.
func (s *Stats) TotalRefactors() int { return sum(s.Refactors) }

func sum(m map[string]int) int {
	total := 0
	for _, n := range m {
		total += n
	}

	return total
}

// add folds another transcript's counts into s.
func (s *Stats) add(o *Stats) {
	s.ToolCalls = merge(s.ToolCalls, o.ToolCalls)
	s.ToolErrors = merge(s.ToolErrors, o.ToolErrors)
	s.SweepCommands = merge(s.SweepCommands, o.SweepCommands)
	s.Refactors = merge(s.Refactors, o.Refactors)
	s.Turns += o.Turns
	s.Retrieval += o.Retrieval
	s.Edit += o.Edit
	s.Shell += o.Shell
	s.Other += o.Other
	s.Prose += o.Prose
	s.RepeatReads += o.RepeatReads
	s.Sweeps += o.Sweeps
	s.StopBlocks += o.StopBlocks
	s.Skipped += o.Skipped
}

func merge(dst, src map[string]int) map[string]int {
	for k, n := range src {
		dst = bump(dst, k, n)
	}

	return dst
}

func bump(m map[string]int, key string, n int) map[string]int {
	if m == nil {
		m = map[string]int{}
	}

	m[key] += n

	return m
}

// Render writes the attribution as text, one fact per line, with the
// per-name counts sorted so two renders of one session are identical.
func (s *Stats) Render(w io.Writer) error {
	var b strings.Builder

	fmt.Fprintf(&b, "turns         %d", s.Turns)

	if s.Subagents > 0 {
		fmt.Fprintf(&b, " (%d subagent transcript(s) included)", s.Subagents)
	}

	fmt.Fprintf(&b, "\n  retrieval   %d\n  edit        %d\n  shell       %d\n  other       %d\n  prose       %d\n",
		s.Retrieval, s.Edit, s.Shell, s.Other, s.Prose)
	fmt.Fprintf(&b, "tool calls    %d%s\n", s.TotalToolCalls(), byName(s.ToolCalls))
	fmt.Fprintf(&b, "tool errors   %d%s\n", s.TotalToolErrors(), byName(s.ToolErrors))
	fmt.Fprintf(&b, "repeat reads  %d\n", s.RepeatReads)
	fmt.Fprintf(&b, "sweeps        %d%s\n", s.Sweeps, byName(s.SweepCommands))
	fmt.Fprintf(&b, "refactors     %d%s\n", s.TotalRefactors(), byName(s.Refactors))
	fmt.Fprintf(&b, "stop blocks   %d\n", s.StopBlocks)
	fmt.Fprintf(&b, "after edits   %d turn(s) after the last file write\n", s.TurnsAfterLastEdit)

	if s.Skipped > 0 {
		fmt.Fprintf(&b, "skipped       %d unreadable line(s)\n", s.Skipped)
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing session stats: %w", err)
	}

	return nil
}

// byName renders a count map as " (a 3, b 1)", largest first.
func byName(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}

	keys := slices.Sorted(maps.Keys(m))
	slices.SortStableFunc(keys, func(a, b string) int { return m[b] - m[a] })

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}

	return " (" + strings.Join(parts, ", ") + ")"
}
