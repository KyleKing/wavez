package tui

// renderHelp lists every layer of controls for the current screen: the
// universal floor (L0/L1) plus that screen's single-key verbs (L2).
func (m Model) renderHelp() string {
	hints := m.currentHints()

	const headerLines = 4

	body := make([]string, 0, len(hints)+headerLines)
	body = append(body,
		"navigation   j/k up/down   g/G top/bottom   Tab/Shift+Tab focus panel",
		"universal    Esc back   ? help   : palette   q quit (Home only)",
		"",
		"this screen:",
	)

	for _, h := range hints {
		body = append(body, "  "+h.key+"  "+h.label)
	}

	return frame(m.width, "help", body, "[esc]"+labelBack, m.th)
}

func (m Model) currentHints() []hint {
	switch m.top() {
	case screenHome:
		return homeHints(m.home.filtering)
	case screenThread:
		return threadHints()
	case screenInbox:
		return []hint{{keyEnter, "answer"}, {"o", labelOpen}, {keyEsc, labelBack}}
	case screenDiagnostics:
		return diagnosticsHints()
	default:
		return nil
	}
}
