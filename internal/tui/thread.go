package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

// Focus indices within Thread view's three panels, cycled by Tab.
const (
	focusTranscript = iota
	focusDiff
	focusInput
)

// threadState is the active thread id, transcript scroll offset, and the
// send input.
type threadState struct {
	activeID     string
	input        textinput.Model
	scrollOffset int
}

func newThreadState() threadState {
	ti := textinput.New()
	ti.Placeholder = "type a message and press enter to send"
	ti.Prompt = ""

	return threadState{input: ti}
}

func (m Model) activeThread() (api.ThreadInfo, bool) {
	for i := range m.threads {
		if m.threads[i].ID == m.thread.activeID {
			return m.threads[i], true
		}
	}

	return api.ThreadInfo{}, false
}

func (m Model) updateThreadKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	pending := m.pendingFor(m.thread.activeID)

	if m.focus == focusInput && m.thread.input.Value() == "" && pending != nil && !pending.Question {
		if mm, cmd, handled := m.threadAnswerKey(s, *pending); handled {
			return mm, cmd
		}
	}

	switch s {
	case "[":
		return m.switchThread(-1)
	case "]":
		return m.switchThread(1)
	case "up":
		if m.focus == focusTranscript {
			m.thread.scrollOffset++

			return m, nil
		}
	case keyDown:
		if m.focus == focusTranscript {
			m.thread.scrollOffset = max(m.thread.scrollOffset-1, 0)

			return m, nil
		}
	case keyEnter:
		if m.focus == focusInput {
			return m.sendThreadInput()
		}
	}

	if m.focus != focusInput {
		return m, nil
	}

	var cmd tea.Cmd
	m.thread.input, cmd = m.thread.input.Update(msg)

	return m, cmd
}

// threadAnswerKey answers a pending permission prompt on the active thread
// directly from y/n/a when the input line is empty, so a user does not have
// to leave Thread view to unblock it.
func (m Model) threadAnswerKey(s string, pending api.PendingInfo) (Model, tea.Cmd, bool) {
	switch s {
	case "y":
		mm, cmd := m.sendAnswer(pending.ID, "", permission.Allow)

		return mm, cmd, true
	case "n":
		mm, cmd := m.sendAnswer(pending.ID, "", permission.Deny)

		return mm, cmd, true
	case "a":
		mm, cmd := m.sendAnswer(pending.ID, "", permission.AllowAlways)

		return mm, cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) switchThread(delta int) (Model, tea.Cmd) {
	if len(m.threads) == 0 {
		return m, nil
	}

	idx := 0

	for i := range m.threads {
		if m.threads[i].ID == m.thread.activeID {
			idx = i

			break
		}
	}

	idx = (idx + delta + len(m.threads)) % len(m.threads)
	m.thread.activeID = m.threads[idx].ID
	m.thread.scrollOffset = 0

	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.subscribe(m.thread.activeID)
	}

	return m, cmd
}

func (m Model) sendThreadInput() (Model, tea.Cmd) {
	text := strings.TrimSpace(m.thread.input.Value())
	if text == "" {
		return m, nil
	}

	m.thread.input.Reset()

	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.send(m.thread.activeID, text)
	}

	return m, cmd
}

func (m Model) renderThread() string {
	info, ok := m.activeThread()
	if !ok {
		return frame(m.width, "thread", []string{m.th.fgMuted.Render("no thread selected")}, keyEsc+" "+labelBack, m.th)
	}

	title := fmt.Sprintf("%s · %s · %s %s/%s · %s%s",
		lastSeg(info.Dir), info.Name, orDash(info.Model), tokensK(info.Context), tokensK(info.Window),
		spend(info.Spend), otherPendingBadge(m.pending, info.ID, m.ascii))

	stacked := m.width < stackedBelowWidth

	body := m.threadBody(info, stacked)
	footer := footerHints(threadHints(), m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

func otherPendingBadge(pending []api.PendingInfo, activeID string, ascii bool) string {
	n := 0

	for i := range pending {
		if pending[i].ThreadID != activeID {
			n++
		}
	}

	if n == 0 {
		return ""
	}

	return fmt.Sprintf(" · %s%d", glyph(event.StateNeedsIn, ascii), n)
}

func tokensK(n int) string {
	const kilo = 1000.0

	return fmt.Sprintf("%.1fk", float64(n)/kilo)
}

func lastSeg(dir string) string {
	i := strings.LastIndexByte(strings.TrimRight(dir, "/"), '/')
	if i < 0 {
		return dir
	}

	return dir[i+1:]
}

// Layout constants for Thread view's fixed chrome: below stackedBelowWidth
// columns the diff pane stacks under the transcript per DESIGN.md, and the
// non-transcript rows (header, ledger, separators, diff, input) consume a
// fixed share of the frame's height.
const (
	stackedBelowWidth = 100
	chromeRows        = 8
	stackedChromeRows = 10
	ledgerLabelWidth  = 8
)

func (m Model) threadBody(info api.ThreadInfo, stacked bool) []string {
	inner := m.width - boxPad
	transcriptHeight := m.height - chromeRows
	if stacked {
		const half = 2
		transcriptHeight = (m.height - stackedChromeRows) / half
	}

	var body []string
	body = append(body, m.th.fgMuted.Render("ledger  "+truncate(ledgerLine(info), inner-ledgerLabelWidth)))

	tr := m.transcripts[info.ID]
	if tr != nil {
		for _, r := range tr.visible(max(transcriptHeight, 1), m.thread.scrollOffset) {
			body = append(body, renderRow(r, inner, m.th))
		}
	}

	sep := strings.Repeat("─", max(inner, 0))
	body = append(body, sep)
	body = append(body, diffLines(tr, inner)...)
	body = append(body, sep, "> "+m.thread.input.View())

	return body
}

func ledgerLine(info api.ThreadInfo) string {
	return info.Step
}

func diffLines(tr *transcript, width int) []string {
	if tr == nil {
		return []string{"  (no changes yet)"}
	}

	paths, stats := tr.changeStats()
	if len(paths) == 0 {
		return []string{"  (no changes yet)"}
	}

	out := make([]string, 0, len(paths))
	for _, path := range paths {
		s := stats[path]
		out = append(out, truncate(fmt.Sprintf("%s  +%d -%d", path, s[0], s[1]), width))
	}

	return out
}

func threadHints() []hint {
	return []hint{
		{keyEnter, "send"},
		{"tab", "panel"},
		{"[", "prev"},
		{"]", "next"},
		{"i", labelInbox},
		{keyEsc, "home"},
		{"?", labelHelp},
	}
}
