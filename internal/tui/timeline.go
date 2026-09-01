package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/bench"
)

// timelineState is the timeline screen's cursor: which turn is selected.
// The turns themselves are reduced from the transcript on every render, so
// the screen can never go stale against the events it sits beside.
type timelineState struct {
	cursor int
}

// updateTimelineKey handles the timeline screen's keys. Esc is handled
// globally by popOrClose, which pops back to the thread view.
func (m Model) updateTimelineKey(_ tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	turns := m.timelineTurns()

	switch s {
	case keyJ, keyDown:
		m.timeline.cursor = min(m.timeline.cursor+1, max(len(turns)-1, 0))
	case keyK, keyUp:
		m.timeline.cursor = max(m.timeline.cursor-1, 0)
	}

	return m, nil
}

func timelineHints() []hint {
	return []hint{
		{"j/k", "pick turn", "move the cursor, which expands that turn's whole tool list"},
		{keyEsc, labelBack, ""},
		{"?", labelHelp, ""},
	}
}

// timelineTurns reduces the active thread's transcript to one row per turn,
// the same reduction bench.Timeline makes of a finished run's log.
func (m Model) timelineTurns() []bench.Turn {
	info, ok := m.activeThread()
	if !ok {
		return nil
	}

	tr := m.transcripts[info.ID]
	if tr == nil {
		return nil
	}

	return bench.Timeline(tr.events)
}

// renderTimeline draws the active thread's turns as a paged version of what
// `wavez -timeline` prints: a bar per turn scaled to the run's longest, with
// the turn under the cursor expanded to name every tool call where the
// printed line truncates at toolsPerRow.
func (m Model) renderTimeline() string {
	inner := m.width - boxPad

	info, ok := m.activeThread()
	if !ok {
		return frame(m.width, "timeline", []string{m.th.fgMuted.Render(msgNoThread)},
			keyEsc+" "+labelBack, m.th)
	}

	turns := m.timelineTurns()

	var body []string
	if len(turns) == 0 {
		body = []string{m.th.fgMuted.Render("no turns recorded")}
	} else {
		body = m.timelineRows(turns, inner)
	}

	title := "timeline · " + info.Name
	footer := footerHints(timelineHints(), inner)

	return frame(m.width, title, body, footer, m.th)
}

// timelineRows windows the turn list to the frame's height and expands the
// cursor's row into the turn's whole tool list. The window is derived from
// the cursor rather than remembered, so it is the same across renders: it
// starts at the earliest turn whose presence still leaves the cursor's row
// (with its expansion) inside the frame.
func (m Model) timelineRows(turns []bench.Turn, inner int) []string {
	m.timeline.cursor = min(max(m.timeline.cursor, 0), len(turns)-1)

	budget := max(m.height-frameBorderRows, 1)

	longest := bench.LongestTurn(turns)

	expanded := timelineExpansion(turns[m.timeline.cursor])
	expandedAt := m.timeline.cursor

	// heights[i] is how many body lines turn i occupies, and prefix[i] the
	// height of every turn before it.
	heights := make([]int, len(turns))
	prefix := make([]int, len(turns)+1)
	for i := range turns {
		heights[i] = 1
		if i == expandedAt {
			heights[i] += len(expanded)
		}

		prefix[i+1] = prefix[i] + heights[i]
	}

	offset := 0
	for prefix[expandedAt+1]-prefix[offset] > budget && offset < expandedAt {
		offset++
	}

	var body []string

	for i := offset; i < len(turns) && len(body)+heights[i] <= budget; i++ {
		selected := i == expandedAt

		previousTier := ""
		if i > 0 {
			previousTier = turns[i-1].Tier
		}

		body = append(body, m.timelineRow(turns[i], longest, previousTier, inner, selected))

		if selected {
			body = append(body, expanded...)
		}
	}

	return body
}

// timelineExpansion is the cursor row's continuation: every tool call the
// turn made, which the printed line truncates to toolsPerRow.
func timelineExpansion(t bench.Turn) []string {
	if len(t.Calls) <= bench.ToolsPerRow {
		return nil
	}

	out := make([]string, 0, len(t.Calls))

	for _, c := range t.Calls {
		line := "      " + c.Tool
		if c.Error {
			line += " ✗" + c.Cause
		}

		out = append(out, line)
	}

	return out
}

func (m Model) timelineRow(t bench.Turn, longest time.Duration, previousTier string, inner int, selected bool) string {
	line := strings.TrimRight(padRight(bench.TurnLine(t, longest, previousTier), inner), " ")

	if selected {
		line = m.th.fgEmphasis.Render(line)
	}

	return line
}
