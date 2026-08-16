package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"daemontui/internal/proto"
)

var (
	styleCursor  = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	stylePending = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	styleAgent   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	styleTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	styleGate    = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	stylePermK   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}
	var body string
	if m.screen == screenHome {
		body = m.renderHome()
	} else {
		body = m.renderThread()
	}
	return body + "\n" + m.renderFooter()
}

func (m Model) renderHome() string {
	var b strings.Builder
	b.WriteString(styleDim.Render("wavez — threads") + "\n\n")
	for i, name := range threadNames {
		glyph := "○"
		style := styleAgent
		if m.pending[name] {
			glyph = "●"
			style = stylePending
		}
		line := fmt.Sprintf("%s %-4s step=%-40s seq=%d", glyph, name, truncate(m.lastText[name], 40), m.seq[name])
		if i == m.cursor {
			line = styleCursor.Render("> " + line)
		} else {
			line = style.Render("  " + line)
		}
		b.WriteString(line + "\n")
	}
	if m.status != "" {
		b.WriteString("\n" + styleDim.Render(m.status))
	}
	return b.String()
}

// renderThread virtualizes the transcript: it slices the in-memory event log
// down to the rows that fit on screen instead of joining and re-laying out
// the full history on every redraw.
func (m Model) renderThread() string {
	name := m.activeThread()
	rows := m.events[name]

	header := styleDim.Render(fmt.Sprintf("thread %s — seq=%d events=%d", name, m.seq[name], len(rows)))
	visible := m.height - 6
	if visible < 1 {
		visible = 1
	}

	end := len(rows) - m.scrollOffset
	if end > len(rows) {
		end = len(rows)
	}
	if end < 0 {
		end = 0
	}
	start := end - visible
	if start < 0 {
		start = 0
	}

	var b strings.Builder
	b.WriteString(header + "\n\n")
	for _, e := range rows[start:end] {
		b.WriteString(renderEvent(e) + "\n")
	}
	b.WriteString("\n" + m.input.View())
	return b.String()
}

func renderEvent(e proto.Event) string {
	switch e.Kind {
	case proto.KindTool:
		return styleTool.Render(fmt.Sprintf("[tool] %s", e.Text))
	case proto.KindGate:
		return styleGate.Render(fmt.Sprintf("-- %s --", e.Text))
	case proto.KindPermission:
		return stylePermK.Render(fmt.Sprintf("[permission] %s", e.Text))
	default:
		return styleAgent.Render(e.Text)
	}
}

func (m Model) renderFooter() string {
	if m.screen == screenHome {
		return styleDim.Render("enter: open  y/n: answer  [/]: cursor  ctrl+c: quit")
	}
	return styleDim.Render("esc: back  [/]: switch thread  up/down: scroll  y/n: answer  enter: send")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
