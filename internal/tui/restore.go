package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
)

// restoreState is the undo confirmation: which thread it targets and the
// work the daemon reported it would discard. It is never open without a
// summary, because a confirmation that does not say what it destroys is
// not a confirmation.
type restoreState struct {
	threadID string
	summary  []string
	// edits is the run's accepted changes, oldest first. Cursor picks how
	// far back the undo goes: 0 is the whole run, and n is the tree as the
	// nth edit left it.
	edits  []api.EditPoint
	cursor int
	open   bool
}

// target is the operation the current selection restores to, empty for the
// run's own baseline.
func (r restoreState) target() string {
	if r.cursor == 0 || r.cursor > len(r.edits) {
		return ""
	}

	return r.edits[r.cursor-1].Op
}

// restoreErrMsg reports a restore the daemon refused or could not finish.
type restoreErrMsg struct{ err error }

// requestRestore asks the daemon what undoing the active thread would
// discard. It sends nothing for a thread that never ran, since there is no
// checkpoint to go back to.
func (m Model) requestRestore() (Model, tea.Cmd) {
	info, ok := m.activeThread()
	if !ok {
		m.status = msgNoThread

		return m, nil
	}
	if info.Checkpoint == "" {
		m.status = info.Name + " has no checkpoint yet"

		return m, nil
	}
	if m.client == nil {
		return m, nil
	}

	m.status = ""

	return m, m.client.restore(info.ID, false)
}

func (m Model) updateRestoreKey(s string) (Model, tea.Cmd) {
	switch s {
	case "y", keyEnter:
		threadID, target := m.restore.threadID, m.restore.target()
		m.restore = restoreState{}
		m.status = "restoring…"

		if m.client == nil {
			return m, nil
		}

		return m, m.client.restoreTo(threadID, target, true)
	case "j", keyDown:
		if m.restore.cursor < len(m.restore.edits) {
			m.restore.cursor++
			m.status = ""

			return m, m.client.restoreTo(m.restore.threadID, m.restore.target(), false)
		}

		return m, nil
	case "k", keyUp:
		if m.restore.cursor > 0 {
			m.restore.cursor--
			m.status = ""

			return m, m.client.restoreTo(m.restore.threadID, m.restore.target(), false)
		}

		return m, nil
	case "n":
		m.restore = restoreState{}
		m.status = "undo canceled"

		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) applyRestore(r api.Restore) {
	if r.Restored {
		m.restore = restoreState{}
		m.status = "restored to checkpoint " + shortCheckpoint(r.Checkpoint) + ", discarded " + statTotal(r.Summary)

		return
	}

	m.restore = restoreState{
		open: true, threadID: r.ThreadID, summary: summaryLines(r.Summary),
		edits: r.Edits, cursor: m.restore.cursor,
	}
}

func summaryLines(summary string) []string {
	var out []string

	for _, line := range strings.Split(strings.TrimRight(summary, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}

	return out
}

// statTotal is the trailing "N files changed, …" line of a diff stat, which
// is the one-line form of what an undo cost.
func statTotal(summary string) string {
	lines := summaryLines(summary)
	if len(lines) == 0 {
		return "nothing"
	}

	return strings.TrimSpace(lines[len(lines)-1])
}

// checkpointDisplay is how much of an operation id is worth showing: jj's
// own logs abbreviate to 12 hex characters.
const checkpointDisplay = 12

func shortCheckpoint(id string) string {
	if len(id) <= checkpointDisplay {
		return id
	}

	return id[:checkpointDisplay]
}

// editChoices lists the run's start and each accepted change, so undo
// reaches one edit rather than only the whole run. The operation ids ride
// on the tool events the harness already wrote.
func (m Model) editChoices(inner int) []string {
	out := make([]string, 0, len(m.restore.edits)+1)
	out = append(out, m.choiceLine(0, "before the run", inner))

	for i, e := range m.restore.edits {
		label := e.Tool + " " + strings.Join(e.Paths, ", ")
		out = append(out, m.choiceLine(i+1, strings.TrimSpace(label), inner))
	}

	return out
}

func (m Model) choiceLine(i int, label string, inner int) string {
	marker := "  "
	if i == m.restore.cursor {
		marker = "> "
	}

	line := truncate(marker+label, inner)
	if i == m.restore.cursor {
		return m.th.statusWarn.Render(line)
	}

	return line
}

func (m Model) renderRestore() string {
	inner := m.width - boxPad

	const chrome = 4

	body := make([]string, 0, len(m.restore.summary)+len(m.restore.edits)+chrome)
	body = append(body, m.th.statusWarn.Render("undo discards this uncommitted work, permanently:"), "")

	for _, line := range m.restore.summary {
		body = append(body, truncate("  "+line, inner))
	}

	if len(m.restore.edits) > 0 {
		body = append(body, "", "undo back to:")
		body = append(body, m.editChoices(inner)...)
	}

	body = append(body, "", "restore?  [y]es  [n]o")

	hints := []hint{{"y", "restore"}, {"n", labelCancel}, {keyEsc, labelCancel}}
	if len(m.restore.edits) > 0 {
		hints = append([]hint{{"j/k", "pick edit"}}, hints...)
	}

	footer := footerHints(hints, inner)

	return frame(m.width, "undo", body, footer, m.th)
}
