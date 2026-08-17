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
	open     bool
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
		threadID := m.restore.threadID
		m.restore = restoreState{}
		m.status = "restoring…"

		if m.client == nil {
			return m, nil
		}

		return m, m.client.restore(threadID, true)
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

	m.restore = restoreState{open: true, threadID: r.ThreadID, summary: summaryLines(r.Summary)}
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

func (m Model) renderRestore() string {
	inner := m.width - boxPad

	const chrome = 4

	body := make([]string, 0, len(m.restore.summary)+chrome)
	body = append(body, m.th.statusWarn.Render("undo discards this uncommitted work, permanently:"), "")

	for _, line := range m.restore.summary {
		body = append(body, truncate("  "+line, inner))
	}

	body = append(body, "", "restore the thread's checkpoint?  [y]es  [n]o")

	footer := footerHints([]hint{{"y", "restore"}, {"n", labelCancel}, {keyEsc, labelCancel}}, inner)

	return frame(m.width, "undo", body, footer, m.th)
}
