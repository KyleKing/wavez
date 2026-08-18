package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

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

// keyCompose expands the composer to the whole frame and back. It is a
// chord rather than a letter because normal mode owns every letter, and
// ctrl+f rather than ctrl+w or ctrl+u because those two delete text in
// insert mode; the only vim binding it costs is a page-down the composer
// has no use for.
const keyCompose = "ctrl+f"

// threadState is the active thread id, transcript scroll offset, and the
// modal composer.
type threadState struct {
	activeID string
	// filter keeps only rows in this category, or every row when catNone
	// ("all"). It is cleared on switchThread, alongside cursor and search,
	// because it names positions in one thread's row list; it survives
	// leaving Thread view for another screen and coming back to the same
	// thread, the way search and cursor already do.
	filter       filterCategory
	search       searchState
	input        vimInput
	scrollOffset int
	// cursor is the transcript row the reader has selected, or -1 when
	// nothing has been selected yet, which keeps the view pinned to the
	// bottom as new events stream in until the reader moves it.
	cursor     int
	diffCursor int
	fullscreen bool
}

func newThreadState(th theme) threadState {
	return threadState{
		input:  newVimInput("press tab to compose, esc for normal mode"),
		search: newSearchState(th),
		cursor: -1,
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

// updateThreadKey routes one key on Thread view. Focus decides whether a
// letter is a verb or a character: the composer owns every key while the
// input panel holds focus, so `d` deletes there and opens the diff pane
// from the other panels, and the screen's verbs need Tab or Esc to reach.
// Modal editing does not change that rule, it only makes the composer's
// half of it modal.
func (m Model) updateThreadKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	if m.thread.search.editing {
		return m.updateSearchKey(msg, s)
	}

	if s == keyCompose {
		return m.toggleCompose(), nil
	}

	if m.focus == focusInput {
		return m.updateComposerKey(msg, s)
	}

	if mm, cmd, handled := m.threadSearchKey(s); handled {
		return mm, cmd
	}

	pending := m.pendingFor(m.thread.activeID)

	if m.focus == focusTranscript && pending != nil && !pending.Question {
		if mm, cmd, handled := m.threadAnswerKey(s, *pending); handled {
			return mm, cmd
		}
	}

	if m.focus == focusTranscript && s == keyEnter {
		return m.toggleCursorRow()
	}

	if mm, cmd, handled := m.threadNavKey(s); handled {
		return mm, cmd
	}

	if mm, handled := m.threadScrollKey(s); handled {
		return mm, nil
	}

	return m, nil
}

// updateComposerKey hands the composer everything the input panel receives.
// Enter sends from the inline row and inserts a newline in fullscreen,
// where the message being written is long enough to need one, and `:`
// reaches the palette from normal mode because that is vim's command line.
func (m Model) updateComposerKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	normal := m.thread.input.mode == modeNormal

	switch {
	case s == keyEnter && !m.thread.fullscreen:
		return m.sendThreadInput()
	case s == ":" && normal:
		m.palette.open = true

		return m, m.palette.input.Focus()
	}

	m.thread.input.handleKey(msg, s)

	return m, nil
}

// toggleCompose expands the composer to the whole frame and back, taking
// focus with it so the key works from any panel. It leaves the mode alone
// when the composer already had focus: resizing the frame is not a reason
// to drop someone out of normal mode.
func (m Model) toggleCompose() Model {
	m.thread.fullscreen = !m.thread.fullscreen
	if m.thread.fullscreen && m.focus != focusInput {
		m.focus = focusInput
		m.thread.input.Focus()
	}

	return m
}

// threadNavKey handles the keys that move between threads and panels.
func (m Model) threadNavKey(s string) (Model, tea.Cmd, bool) {
	if mm, cmd, handled := m.threadPinKey(s); handled {
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
		m.focus = focusDiff

		return m, m.requestDiff(), true
	case "a":
		if m.focus != focusDiff {
			return m, nil, false
		}

		return m.askLine(), nil, true
	case "f":
		mm, cmd := m.openNewThread(m.thread.activeID)

		return mm, cmd, true
	case "u":
		mm, cmd := m.requestRestore()

		return mm, cmd, true
	case "c":
		return m.cycleKindFilter(), nil, true
	case "s":
		m.push(screenSummary)

		return m, nil, true
	default:
		return m, nil, false
	}
}

// cycleKindFilter steps the transcript's kind filter to the next category
// (edits, shells, gates, permissions, answers, then "all" again), and snaps
// the cursor onto a visible row when the step just hid the one it was on.
func (m Model) cycleKindFilter() Model {
	m.thread.filter = nextFilterCategory(m.thread.filter)

	tr := m.transcripts[m.thread.activeID]
	if tr == nil {
		return m
	}

	m.thread.cursor = snapCursor(tr.visibleRows(m.thread.filter), m.thread.cursor)

	return m.clampCursorVisible(tr)
}

// snapCursor finds cursor's nearest surviving row in visible (ascending row
// indices): cursor itself when the filter kept it, else the closest one
// before it, else the first one after, else -1 when nothing survived. It
// keeps the cursor answering the same question ("what am I looking at")
// across a filter change instead of resetting to the bottom every time.
func snapCursor(visible []int, cursor int) int {
	if cursor < 0 {
		return cursor
	}

	if len(visible) == 0 {
		return -1
	}

	i := sort.Search(len(visible), func(i int) bool { return visible[i] >= cursor })
	if i < len(visible) && visible[i] == cursor {
		return cursor
	}

	if i > 0 {
		return visible[i-1]
	}

	return visible[0]
}

// threadPinKey handles the two keys that pin how the next turn runs.
func (m Model) threadPinKey(s string) (Model, tea.Cmd, bool) {
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

// threadScrollKey moves whichever pane has focus. The transcript no longer
// scrolls directly: j/k and the arrows move a row cursor, and the offset
// follows it so a wrapped or expanded row's lines land inside the window
// together rather than a row-blind scroll stranding most of a long row off
// screen.
func (m Model) threadScrollKey(s string) (Model, bool) {
	switch {
	case (s == keyUp || s == keyK) && m.focus == focusTranscript:
		return m.moveCursor(-1), true
	case (s == keyDown || s == keyJ) && m.focus == focusTranscript:
		return m.moveCursor(1), true
	case s == keyUp && m.focus == focusDiff:
		m.thread.diffCursor = max(m.thread.diffCursor-1, 0)
	case s == keyDown && m.focus == focusDiff:
		m.thread.diffCursor = min(m.thread.diffCursor+1, max(len(m.diffs[m.thread.activeID])-1, 0))
	default:
		return m, false
	}

	return m, true
}

// moveCursor steps the transcript cursor by delta rows among those the
// active kind filter keeps, clamping at both ends. The first move from no
// cursor lands on the last visible row rather than applying delta from an
// assumed position, since that is the row the reader is already looking at
// while the view is pinned to the bottom.
func (m Model) moveCursor(delta int) Model {
	tr := m.transcripts[m.thread.activeID]
	if tr == nil {
		return m
	}

	visible := tr.visibleRows(m.thread.filter)
	if len(visible) == 0 {
		return m
	}

	pos := slices.Index(visible, m.thread.cursor)

	switch {
	case m.thread.cursor < 0, pos < 0:
		pos = len(visible) - 1
	default:
		pos = min(max(pos+delta, 0), len(visible)-1)
	}

	m.thread.cursor = visible[pos]

	return m.clampCursorVisible(tr)
}

// toggleCursorRow folds or unfolds the row under the transcript cursor and
// keeps it fully visible either way.
func (m Model) toggleCursorRow() (Model, tea.Cmd) {
	tr := m.transcripts[m.thread.activeID]
	if tr == nil || m.thread.cursor < 0 || !tr.toggle(m.thread.cursor) {
		return m, nil
	}

	return m.clampCursorVisible(tr), nil
}

// transcriptWidth is the column budget transcript rows wrap into, matching
// threadBody's inner width so line counts computed for scrolling agree
// with what actually renders.
func (m Model) transcriptWidth() int {
	return max(m.width-boxPad, 0)
}

// clampCursorVisible adjusts scrollOffset so every rendered line of the
// cursor's row sits inside the transcript window, moving it as little as
// possible.
func (m Model) clampCursorVisible(tr *transcript) Model {
	width := m.transcriptWidth()
	height := m.transcriptHeight()
	lineCount := tr.lineCount(width, m.thread.filter)

	lo, hi := rowLineSpan(tr, width, m.thread.filter, m.thread.cursor, lineCount)
	if lo >= hi {
		return m
	}

	m.thread.scrollOffset = clampOffsetToRow(m.thread.scrollOffset, height, lineCount, lo, hi)

	return m
}

// rowLineSpan finds the [lo, hi) rendered-line range row occupies at width
// under filter. It binary-searches rather than scanning every line, since
// rowAtLine reports rows in non-decreasing order as line grows and a
// transcript can run to hundreds of rows.
//
//nolint:gocritic // named returns are forbidden
func rowLineSpan(tr *transcript, width int, filter filterCategory, row, lineCount int) (int, int) {
	lo := sort.Search(lineCount, func(i int) bool { return tr.rowAtLine(width, filter, i) >= row })
	hi := sort.Search(lineCount, func(i int) bool { return tr.rowAtLine(width, filter, i) > row })

	return lo, hi
}

// clampOffsetToRow nudges a bottom-relative line offset just enough to
// bring [lo, hi) fully into a window of height lines out of lineCount
// total, leaving it unchanged when the range is already visible. A range
// taller than the window shows from lo rather than being scrolled past.
func clampOffsetToRow(offset, height, lineCount, lo, hi int) int {
	if height <= 0 || lineCount <= 0 {
		return 0
	}

	maxOffset := max(lineCount-height, 0)

	if hi-lo >= height {
		return min(max(lineCount-lo-height, 0), maxOffset)
	}

	end := lineCount - offset
	start := max(end-height, 0)

	switch {
	case lo < start:
		end = lo + height
	case hi > end:
		end = hi
	default:
		return min(max(offset, 0), maxOffset)
	}

	return min(max(lineCount-end, 0), maxOffset)
}

// threadAnswerKey answers a pending permission prompt on the active thread
// directly from y/n/a, so a user does not have to leave Thread view to
// unblock it. It reads only from the transcript panel, where the permission
// row lives: on the composer `a` starts an append and would grant
// allow-always instead, and widening a permission by mistyping is the one
// outcome worth costing a Tab to prevent.
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
	m.thread.cursor = -1
	m.thread.diffCursor = 0
	m.thread.filter = catNone
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

	title := fmt.Sprintf("%s · %s · %s %s/%s · %s%s%s",
		lastSeg(info.Dir), info.Name, m.activeModel(info), tokensK(info.Context), tokensK(info.Window),
		spend(info.Spend), filterBadge(m.thread.filter), otherPendingBadge(m.pending, info.ID, m.ascii))

	body := m.threadBody(info)
	footer := footerHints(threadHints(m.thread.search, m.focus, m.thread.filter), m.width-boxPad)

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
		rows := tr.render(renderOpts{
			width: inner, height: m.transcriptHeight(), offset: m.thread.scrollOffset,
			cursor: m.thread.cursor, theme: m.th, query: m.thread.search.query, filter: m.thread.filter,
		})
		if len(rows) == 0 && m.thread.filter != catNone && tr.count() > 0 {
			rows = []string{m.th.fgMuted.Render("no " + string(m.thread.filter) + " in this thread")}
		}

		body = append(body, rows...)
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

	body = append(body, sep, m.thread.input.inlineView(m.th))

	return body
}

// composeChrome is the rows renderCompose spends on the frame's border and
// the composer's own status line.
const composeChrome = 3

// renderCompose is the fullscreen composer: the whole frame is the message
// being written, with the same modal editing the inline row has.
func (m Model) renderCompose() string {
	title := "compose"
	if info, ok := m.activeThread(); ok {
		title = "compose · " + info.Name
	}

	body := m.thread.input.composeBody(m.th, m.width-boxPad, m.height-composeChrome)
	footer := footerHints([]hint{
		{keyCompose, "inline"},
		{keyEnter, "newline"},
		{keyEsc, "normal"},
		{"i", "insert"},
		{keyTab, "snippet"},
	}, m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
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
const threadHintTail = 12

// threadHints is priority ordered; footerHints drops from the tail. The
// composer's own keys lead while it holds focus, the way a live search
// query leads while one is set, because that is when they are reachable.
// Enter's own meaning is per-panel: it sends from the composer and toggles
// the row under the cursor from the transcript, so the hint names whichever
// is true of the panel that is actually focused. It binds to nothing on the
// diff pane, where no hint names it.
func threadHints(search searchState, focus int, filter filterCategory) []hint {
	if search.editing {
		return []hint{{keyEnter, labelApply}, {keyEsc, labelCancel}}
	}

	if focus == focusInput {
		return []hint{
			{keyEnter, labelSend},
			{keyCompose, "fullscreen"},
			{keyEsc, "normal mode"},
			{keyTab, labelPanel},
			{"?", labelHelp},
		}
	}

	var enter []hint
	if focus == focusTranscript {
		enter = []hint{{keyEnter, "toggle"}}
	}

	head := make([]hint, 0, len(enter)+2)
	head = append(head, enter...)
	head = append(head, hint{keyTab, labelPanel}, hint{"/", "search"})
	back := []hint{{keyEsc, labelHome}}

	if filter != catNone {
		back = []hint{{keyEsc, "clear filter"}}
	}

	// A live query puts its keys first: footerHints drops from the tail, and
	// Esc is the only way back out of the highlight.
	if search.query != "" {
		head = make([]hint, 0, len(enter)+4)
		head = append(head,
			hint{"n", "next match"},
			hint{"N", "prev match"},
			hint{keyEsc, "clear search"},
		)
		head = append(head, enter...)
		head = append(head, hint{keyTab, labelPanel})
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
		hint{"F", "fuzzy"},
		hint{"c", filterHintLabel(filter)},
		hint{"s", "summary"},
	)
	hints = append(hints, back...)

	return append(hints, hint{"?", labelHelp})
}

// filterBadge names an active kind filter in the header. A filter that keeps
// one row type can empty the transcript, so what is hiding the rest belongs
// where it stays visible rather than in a footer hint the width may drop.
func filterBadge(filter filterCategory) string {
	if filter == catNone {
		return ""
	}

	return " · " + string(filter) + " only"
}

// filterHintLabel names what `c` does next: the category it would switch
// to keeping, or that it would clear back to every row once every category
// has been cycled through.
func filterHintLabel(filter filterCategory) string {
	if filter == catNone {
		return "filter"
	}

	return "filter:" + string(filter)
}
