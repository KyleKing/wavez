package tui

// renderHelp lists every layer of controls for the current screen: the
// universal floor (L0/L1) plus that screen's single-key verbs (L2).
func (m Model) renderHelp() string {
	hints := m.currentHints()

	const headerLines = 4

	body := make([]string, 0, len(hints)+headerLines+len(composerHelp()))
	body = append(body,
		"navigation   j/k up/down   g/G top/bottom   Tab/Shift+Tab focus panel",
		"universal    Esc back   ? help   : palette   q quit (Home only)",
	)

	if m.top() == screenThread {
		body = append(body, composerHelp()...)
	}

	body = append(body, "", "this screen:")

	for _, h := range hints {
		body = append(body, "  "+h.key+"  "+h.label)
	}

	return frame(m.width, "help", body, "[esc]"+labelBack, m.th)
}

// composerHelp is the message composer's map. Editing is modal and has no
// non-vim fallback, so the floor has to be written down somewhere.
func composerHelp() []string {
	return []string{
		"composer     i a I A o O insert   Esc normal   Ctrl+F fullscreen",
		"  motions    h j k l   w b e   0 $   gg G",
		"  edits      x  d{motion}  dd  D  c{motion}  cw  C   u undo   p paste",
	}
}

func (m Model) currentHints() []hint {
	switch m.top() {
	case screenHome:
		return homeHints(m.home.filtering)
	case screenThread:
		return threadHints(m.thread.search, m.focus == focusInput)
	case screenInbox:
		return []hint{{keyEnter, "answer"}, {"o", labelOpen}, {keyEsc, labelBack}}
	case screenDiagnostics:
		return diagnosticsHints()
	case screenSchedule:
		return scheduleHints(m.sched.leases)
	default:
		return nil
	}
}
