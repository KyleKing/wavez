package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderHelp lists every layer of controls for the current screen: the
// universal floor (L0/L1) plus that screen's single-key verbs (L2). The
// "this screen" entries flow into as many columns as the width fits, and the
// whole body is sized from the lines that actually render so the frame never
// grows taller than the terminal at the app's 80x24 floor.
func (m Model) renderHelp() string {
	hints := m.currentHints()

	body := make([]string, 0, 8)
	body = append(body,
		"navigation   j/k up/down   g/G top/bottom   Tab/Shift+Tab focus panel",
		"universal    Esc back   ? help   : palette   q quit (Home only)",
	)

	if m.top() == screenThread {
		body = append(body, composerHelp()...)
	}

	body = append(body, "", "this screen:")
	body = append(body, hintLines(hints, m.width-boxPad)...)

	// A screen that grows more hints than the columns can fold is cut here
	// rather than rendered off the bottom, taking the frame's rules with it.
	if fits := max(m.height-frameBorderRows, 0); len(body) > fits {
		body = body[:fits]
	}

	return frame(m.width, "help", body, "[esc]"+labelBack, m.th)
}

// hintLines lays hints out in aligned columns: however many copies of the
// widest entry fit side by side across width, so a help list stays inside the
// height instead of running one key per row off the bottom.
func hintLines(hints []hint, width int) []string {
	if len(hints) == 0 || width < 1 {
		return nil
	}

	cells := make([]string, len(hints))

	cellW := 0
	for i, h := range hints {
		text := h.phrase
		if text == "" {
			text = h.label
		}

		cells[i] = h.key + hintGutter + text
		cellW = max(cellW, lipgloss.Width(cells[i]))
	}

	cols := max(width/(cellW+len(hintGutter)), 1)

	var lines []string
	for start := 0; start < len(cells); start += cols {
		line := make([]string, 0, cols)
		for _, c := range cells[start:min(start+cols, len(cells))] {
			line = append(line, padRight(c, cellW))
		}

		lines = append(lines, hintGutter+strings.Join(line, hintGutter))
	}

	return lines
}

// hintGutter separates a key from its phrase and one column from the next,
// so both readings of the gap are the same width.
const hintGutter = "  "

// composerHelp is the message composer's map. Editing is modal and has no
// non-vim fallback, so the floor has to be written down somewhere.
func composerHelp() []string {
	return []string{
		"composer     i a I A o O insert   Esc normal   Ctrl+F fullscreen",
		"  motions    h j k l   w b e   0 $   gg G",
		"  edits      x  d{motion}  dd  D  c{motion}  cw  C   u undo   p paste",
		"  snippets   Tab expands a saved snippet (fullscreen, insert mode)",
	}
}

func (m Model) currentHints() []hint {
	switch m.top() {
	case screenHome:
		return homeHints(m.home.filtering)
	case screenThread:
		return threadHints(m.thread.search, m.focus, m.thread.filter, m.thread.input.mode)
	case screenInbox:
		return []hint{{keyEnter, "answer", ""}, {"o", labelOpen, ""}, {keyEsc, labelBack, ""}}
	case screenDiagnostics:
		return diagnosticsHints(m.diagUI.drilled)
	case screenModels:
		return m.modelHints()
	case screenSchedule:
		return scheduleHints(m.sched.leases)
	case screenRoutines:
		return routinesHints()
	case screenSummary:
		return summaryHints()
	case screenTimeline:
		return timelineHints()
	default:
		return nil
	}
}
