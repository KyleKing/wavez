package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// View satisfies tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true

	return v
}

func (m Model) render() string {
	if !m.ready {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return tooSmall(m.width, m.height)
	}
	if m.help {
		return m.renderHelp()
	}
	if m.goal {
		return m.renderGoal()
	}
	if m.palette.open {
		return m.renderPalette()
	}
	if m.restore.open {
		return m.renderRestore()
	}

	if m.composing() {
		return m.renderCompose()
	}

	out := m.renderScreen()
	if m.toast.current != "" {
		out = m.applyToast(out)
	}

	return out
}

func (m Model) renderScreen() string {
	switch m.top() {
	case screenHome:
		return m.renderHome()
	case screenThread:
		return m.renderThread()
	case screenInbox:
		return m.renderInbox()
	case screenDiagnostics:
		return m.renderDiagnostics()
	case screenModels:
		return m.renderModels()
	case screenNewThread:
		return m.renderNewThread()
	case screenRoutines:
		return m.renderRoutines()
	case screenSchedule:
		return m.renderSchedule()
	case screenSummary:
		return m.renderSummary()
	default:
		return ""
	}
}

// tooSmall is the one message that has to fit a terminal too small for the
// interface, so it is short lines rather than a sentence: the sentence it
// replaced was 99 columns and clipped mid-word in every terminal that saw it.
func tooSmall(width, height int) string {
	return "wavez needs " + size(minWidth, minHeight) + "\n" +
		"this terminal is " + size(width, height) + "\n" +
		"resize to continue"
}

func size(width, height int) string {
	return strconv.Itoa(width) + "x" + strconv.Itoa(height)
}
