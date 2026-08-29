package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// summaryCategories is the order the "what was done" view groups rows in,
// per DESIGN.md's Thread view. It excludes catNone: a row that belongs to
// none of the named categories (a user message, an agent note, a read-only
// tool call) is not audit-relevant the way an edit or a gate verdict is, so
// it is left out of the summary rather than given a catch-all "other"
// section.
var summaryCategories = []filterCategory{catEdit, catShell, catGate, catPermission, catAnswer}

// updateSummaryKey handles Summary view's keys. Esc is handled globally by
// popOrClose, which pops the screen stack; there is nothing else to do here
// yet, since the view is a static, read-only grouping of rows already held
// by the active thread's transcript.
func (m Model) updateSummaryKey(_ string) (Model, tea.Cmd) {
	return m, nil
}

func summaryHints() []hint {
	return []hint{{keyEsc, labelBack, ""}, {"?", labelHelp, ""}}
}

// renderSummary renders the active thread's rows grouped by category
// instead of by time, so auditing a finished run reads by kind: every edit
// together, every shell command together, and so on.
func (m Model) renderSummary() string {
	info, ok := m.activeThread()
	if !ok {
		return frame(m.width, "summary", []string{m.th.fgMuted.Render(msgNoThread)}, keyEsc+" "+labelBack, m.th)
	}

	inner := m.width - boxPad
	tr := m.transcripts[info.ID]

	var body []string
	for _, cat := range summaryCategories {
		body = append(body, m.summarySection(tr, cat, inner)...)
	}

	if len(body) == 0 {
		body = []string{m.th.fgMuted.Render("nothing to audit yet")}
	}

	title := "summary · " + info.Name
	footer := footerHints(summaryHints(), inner)

	return frame(m.width, title, body, footer, m.th)
}

// summarySection renders one category's heading and rows, or nothing when
// the thread has no rows in it: an empty thread's summary has no gates
// heading to sit above zero gates.
func (m Model) summarySection(tr *transcript, cat filterCategory, width int) []string {
	if tr == nil {
		return nil
	}

	rows := tr.visibleRows(cat)
	if len(rows) == 0 {
		return nil
	}

	out := make([]string, 0, len(rows)+1)
	out = append(out, m.th.fgEmphasis.Render(strings.ToUpper(string(cat))))

	const rowLeading = "  " // indent ahead of the label, one space behind it

	for _, i := range rows {
		label, style := rowLabel(tr.rows[i].kind, m.th)
		gutter := len(rowLeading) + len(label) + 1
		out = append(out, rowLeading+style.Render(label)+" "+truncate(rowText(tr.rows[i]), width-gutter))
	}

	return append(out, "")
}
