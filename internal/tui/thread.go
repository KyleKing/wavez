package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
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
	search       searchState
	scrollOffset int
	diffCursor   int
}

func newThreadState(th theme) threadState {
	return threadState{
		input:  th.newInput("type a message and press enter to send"),
		search: newSearchState(th),
	}
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
	if m.thread.search.editing {
		return m.updateSearchKey(msg, s)
	}

	if mm, cmd, handled := m.threadSearchKey(s); handled {
		return mm, cmd
	}

	pending := m.pendingFor(m.thread.activeID)

	if m.focus == focusInput && m.thread.input.Value() == "" && pending != nil && !pending.Question {
		if mm, cmd, handled := m.threadAnswerKey(s, *pending); handled {
			return mm, cmd
		}
	}

	if mm, cmd, handled := m.threadNavKey(s); handled {
		return mm, cmd
	}

	if mm, handled := m.threadScrollKey(s); handled {
		return mm, nil
	}

	if s == keyEnter && m.focus == focusInput {
		return m.sendThreadInput()
	}

	if m.focus != focusInput {
		return m, nil
	}

	var cmd tea.Cmd
	m.thread.input, cmd = m.thread.input.Update(msg)

	return m, cmd
}

// threadNavKey handles the keys that move between threads and panels. Each
// letter key is inert whenever the input panel holds focus, so a message
// may start with any letter. Focus, not the input's contents, is the test:
// keying off a non-empty value ate the first character of every message.
func (m Model) threadNavKey(s string) (Model, tea.Cmd, bool) {
	typing := m.focus == focusInput

	if mm, cmd, handled := m.threadPinKey(s, typing); handled {
		return mm, cmd, true
	}

	switch s {
	case "[":
		mm, cmd := m.switchThread(-1)

		return mm, cmd, true
	case "]":
		mm, cmd := m.switchThread(1)

		return mm, cmd, true
	case "d":
		if typing {
			return m, nil, false
		}

		m.focus = focusDiff

		return m, m.requestDiff(), true
	case "a":
		if m.focus != focusDiff {
			return m, nil, false
		}

		return m.askLine(), nil, true
	case "f":
		if typing {
			return m, nil, false
		}

		mm, cmd := m.openNewThread(m.thread.activeID)

		return mm, cmd, true
	case "u":
		if typing {
			return m, nil, false
		}

		mm, cmd := m.requestRestore()

		return mm, cmd, true
	default:
		return m, nil, false
	}
}

// threadPinKey handles the two keys that pin how the next turn runs. They
// are inert while a message is being typed, like every other letter verb on
// this screen.
func (m Model) threadPinKey(s string, typing bool) (Model, tea.Cmd, bool) {
	if typing {
		return m, nil, false
	}

	switch s {
	case "t":
		mm, cmd := m.cycleThinking()

		return mm, cmd, true
	case "m":
		mm, cmd := m.cycleRoute()

		return mm, cmd, true
	default:
		return m, nil, false
	}
}

// threadScrollKey moves whichever pane has focus.
func (m Model) threadScrollKey(s string) (Model, bool) {
	switch {
	case s == "up" && m.focus == focusTranscript:
		m.thread.scrollOffset++
	case s == "up" && m.focus == focusDiff:
		m.thread.diffCursor = max(m.thread.diffCursor-1, 0)
	case s == keyDown && m.focus == focusTranscript:
		m.thread.scrollOffset = max(m.thread.scrollOffset-1, 0)
	case s == keyDown && m.focus == focusDiff:
		m.thread.diffCursor = min(m.thread.diffCursor+1, max(len(m.diffs[m.thread.activeID])-1, 0))
	default:
		return m, false
	}

	return m, true
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
	m.thread.diffCursor = 0
	m.clearSearch()

	if m.client == nil {
		return m, nil
	}

	return m, tea.Batch(m.client.subscribe(m.thread.activeID), m.requestDiff())
}

// requestDiff asks the daemon for the active thread's change set. The diff
// is fetched rather than streamed because it is unbounded in a way an event
// stream should not be.
func (m Model) requestDiff() tea.Cmd {
	if m.client == nil || m.thread.activeID == "" {
		return nil
	}

	return m.client.diff(m.thread.activeID)
}

// askLine turns the selected diff row into a question anchored at that
// line, leaving it in the input for the user to finish rather than sending
// it, so the anchor is a starting point and not a guess at the question.
func (m Model) askLine() Model {
	rows := m.diffs[m.thread.activeID]
	if m.thread.diffCursor >= len(rows) {
		return m
	}

	anchor := rows[m.thread.diffCursor].anchor()
	if anchor == "" {
		return m
	}

	m.focus = focusInput
	m.thread.input.SetValue("about " + anchor + ": ")
	m.thread.input.Focus()

	return m
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
		lastSeg(info.Dir), info.Name, m.activeModel(info), tokensK(info.Context), tokensK(info.Window),
		spend(info.Spend), otherPendingBadge(m.pending, info.ID, m.ascii))

	body := m.threadBody(info)
	footer := footerHints(threadHints(m.thread.search), m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

// activeModel names the model serving this thread. ThreadInfo.Model carries
// only an override, so a thread without one is served by the daemon's local
// model rather than by nothing. A pinned tier is named instead of the model,
// since the daemon reports no name for the hosted one and a pin is what
// decides the tier either way.
func (m Model) activeModel(info api.ThreadInfo) string {
	switch info.Override {
	case router.ChoiceHosted:
		return "hosted·pinned"
	case router.ChoiceLocal:
		return m.servingModel(info) + "·pinned"
	default:
		return m.servingModel(info)
	}
}

func (m Model) servingModel(info api.ThreadInfo) string {
	if info.Model != "" {
		return info.Model
	}

	return orDash(m.diag.LocalModel)
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

// transcriptHeight is the row budget the transcript window gets after the
// frame's fixed chrome and whatever optional lines are showing.
func (m Model) transcriptHeight() int {
	height := m.height - chromeRows
	if m.width < stackedBelowWidth {
		const half = 2
		height = (m.height - stackedChromeRows) / half
	}

	if m.status != "" {
		height--
	}

	if m.thread.search.visible() {
		height--
	}

	return max(height, 1)
}

func (m Model) threadBody(info api.ThreadInfo) []string {
	inner := m.width - boxPad

	var body []string
	body = append(body, m.th.fgMuted.Render("ledger  "+truncate(ledgerLine(info), inner-ledgerLabelWidth)))

	tr := m.transcripts[info.ID]
	if tr != nil {
		for _, r := range tr.visible(m.transcriptHeight(), m.thread.scrollOffset) {
			body = append(body, renderRow(r, inner, m.th, m.thread.search.query))
		}
	}

	sep := strings.Repeat("─", max(inner, 0))
	body = append(body, sep)
	body = append(body, m.diffPane(inner)...)
	if m.status != "" {
		body = append(body, m.th.statusWarn.Render(truncate(m.status, inner)))
	}

	if m.thread.search.visible() {
		body = append(body, m.searchLine(inner))
	}

	body = append(body, sep, "> "+m.thread.input.View())

	return body
}

func ledgerLine(info api.ThreadInfo) string {
	return info.Step
}

// diffPane renders the active thread's hunks, falling back to the change
// summary the transcript already carries while the daemon's diff is still
// in flight, so the pane never goes blank between requests.
func (m Model) diffPane(width int) []string {
	rows := m.diffs[m.thread.activeID]
	if len(rows) == 0 {
		return changeSummary(m.transcripts[m.thread.activeID], width)
	}

	const paneHeight = 6

	start := max(min(m.thread.diffCursor-paneHeight/2, len(rows)-paneHeight), 0)
	end := min(start+paneHeight, len(rows))

	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, m.renderDiffRow(rows[i], i == m.thread.diffCursor && m.focus == focusDiff, width))
	}

	return out
}

func (m Model) renderDiffRow(r diffRow, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = "› "
	}

	text := truncate(marker+r.Text, width)

	switch r.Kind {
	case diffFile, diffHunk:
		return m.th.fgEmphasis.Render(text)
	case diffAdd:
		return m.th.statusOK.Render(text)
	case diffRemove:
		return m.th.statusErr.Render(text)
	case diffContext:
		return m.th.fgMuted.Render(text)
	default:
		return text
	}
}

func changeSummary(tr *transcript, width int) []string {
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

// threadHintTail is the count of hints threadHints appends after its head.
const threadHintTail = 9

func threadHints(search searchState) []hint {
	if search.editing {
		return []hint{{keyEnter, "apply"}, {keyEsc, labelCancel}}
	}

	head := []hint{{keyEnter, "send"}, {keyTab, "panel"}, {"/", "search"}}
	back := []hint{{keyEsc, "home"}}

	// A live query puts its keys first: footerHints drops from the tail, and
	// Esc is the only way back out of the highlight.
	if search.query != "" {
		head = []hint{
			{"n", "next match"},
			{"N", "prev match"},
			{keyEsc, "clear search"},
			{keyEnter, "send"},
			{keyTab, "panel"},
		}
		back = nil
	}

	hints := make([]hint, 0, len(head)+len(back)+threadHintTail)
	hints = append(hints, head...)
	hints = append(hints,
		hint{"d", "diff"},
		hint{"a", "ask-line"},
		hint{"f", "fork"},
		hint{"[", "prev"},
		hint{"]", "next"},
		hint{"i", labelInbox},
		hint{"u", "undo"},
		hint{"m", "route"},
		hint{"t", "think"},
	)
	hints = append(hints, back...)

	return append(hints, hint{"?", labelHelp})
}
