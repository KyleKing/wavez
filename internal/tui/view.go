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
	if m.palette.open {
		return m.renderPalette()
	}

	switch m.top() {
	case screenHome:
		return m.renderHome()
	case screenThread:
		return m.renderThread()
	case screenInbox:
		return m.renderInbox()
	case screenDiagnostics:
		return m.renderDiagnostics()
	default:
		return ""
	}
}

func tooSmall(width, height int) string {
	return "wavez needs at least 80x24 to render (currently " +
		strconv.Itoa(width) + "x" + strconv.Itoa(height) + "). Resize your terminal."
}
