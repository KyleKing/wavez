package tui

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

// inboxState is the inbox list's cursor and its in-place answer field for
// questions (permission prompts answer directly on y/n/a).
type inboxState struct {
	answerInput textinput.Model
	cursor      int
	answering   bool
}

func newInboxState(th theme) inboxState {
	return inboxState{answerInput: th.newInput("type an answer")}
}

// inboxRows returns pending prompts oldest first, across every thread.
func (m Model) inboxRows() []api.PendingInfo {
	rows := make([]api.PendingInfo, len(m.pending))
	copy(rows, m.pending)

	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Asked.Before(rows[j-1].Asked); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}

	return rows
}

func (m Model) updateInboxKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	rows := m.inboxRows()
	if len(rows) == 0 {
		return m, nil
	}

	cursor := min(max(m.inbox.cursor, 0), len(rows)-1)
	row := rows[cursor]

	if m.inbox.answering {
		return m.inboxAnswerText(msg, s, row)
	}

	switch s {
	case keyJ, keyDown:
		m.inbox.cursor = cursor + 1
	case "k", "up":
		m.inbox.cursor = max(cursor-1, 0)
	case "o", keyEnter:
		return m.openInboxRow(row)
	case "y", "n", "a":
		return m.answerInboxRow(row, s)
	}

	m.inbox.cursor = min(max(m.inbox.cursor, 0), len(rows)-1)

	return m, nil
}

func (m Model) openInboxRow(row api.PendingInfo) (Model, tea.Cmd) {
	m.thread.activeID = row.ThreadID
	m.push(screenThread)

	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.subscribe(row.ThreadID)
	}

	return m, cmd
}

// answerInboxRow answers a permission prompt directly from y/n/a, or, for a
// question, switches to the typed-answer field.
func (m Model) answerInboxRow(row api.PendingInfo, s string) (Model, tea.Cmd) {
	if !row.Question {
		switch s {
		case "y":
			return m.sendPendingAnswer(row.ID, "", permission.Allow)
		case "n":
			return m.sendPendingAnswer(row.ID, "", permission.Deny)
		case "a":
			return m.sendPendingAnswer(row.ID, "", permission.AllowAlways)
		}
	}

	m.inbox.answering = true

	return m, m.inbox.answerInput.Focus()
}

func (m Model) inboxAnswerText(msg tea.KeyPressMsg, s string, row api.PendingInfo) (Model, tea.Cmd) {
	if s == keyEnter {
		text := m.inbox.answerInput.Value()
		m.inbox.answerInput.Reset()
		m.inbox.answering = false

		return m.sendPendingAnswer(row.ID, text, permission.Allow)
	}

	var cmd tea.Cmd
	m.inbox.answerInput, cmd = m.inbox.answerInput.Update(msg)

	return m, cmd
}

func (m Model) sendPendingAnswer(promptID, text string, decision permission.Decision) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.answer(promptID, text, decision)
	}

	return m, cmd
}

const (
	inboxThreadColWidth = 24
	inboxActionColWidth = 20
)

func (m Model) renderInbox() string {
	rows := m.inboxRows()

	title := fmt.Sprintf("inbox · %d waiting", len(rows))

	var body []string
	for i := range rows {
		r := &rows[i]

		answers := "[y] [n] [a]"
		if r.Question {
			answers = "> " + r.Detail
			if i == m.cappedInboxCursor(len(rows)) && m.inbox.answering {
				answers = "> " + m.inbox.answerInput.View()
			}
		}

		line := fmt.Sprintf("%s %-24s %-8s %-20s %s",
			glyph(event.StateNeedsIn, m.ascii), truncate(r.Thread, inboxThreadColWidth), r.Tool,
			truncate(r.Action, inboxActionColWidth), answers)

		if i != m.cappedInboxCursor(len(rows)) {
			body = append(body, m.th.fgDefault.Render("  "+line))
			body = append(body, m.parkedStepLine(r.Step)...)

			continue
		}

		body = append(body, m.th.accent.Render("> "+line))
		body = append(body, m.parkedStepLine(r.Step)...)
		if r.Reason != "" {
			body = append(body, m.th.fgMuted.Render(truncate("    "+r.Reason, m.width-boxPad)))
		}
	}

	if len(rows) == 0 {
		body = append(body, m.th.fgMuted.Render("nothing waiting"))
	}

	footer := footerHints([]hint{{keyEnter, "answer"}, {"o", labelOpen}, {keyEsc, labelBack}}, m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

// parkedStepLine renders what a row's thread was doing just before it
// parked, a second muted line under the row (the row itself is already
// full width with the thread name, tool, detail, and answers). A row with
// no step (a question asked with none yet recorded) adds no line.
func (m Model) parkedStepLine(step string) []string {
	if step == "" {
		return nil
	}

	return []string{m.th.fgMuted.Render(truncate("    "+step, m.width-boxPad))}
}

func (m Model) cappedInboxCursor(n int) int {
	if n == 0 {
		return 0
	}

	return min(max(m.inbox.cursor, 0), n-1)
}
