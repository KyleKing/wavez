package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

// homeState is Home's list cursor, filter, per-row expansion, and scope.
type homeState struct {
	expanded map[string]bool
	// selected is the bulk-action set, keyed by thread ID the way expanded
	// is. `*` fills it from the filtered rows, space toggles one row, and
	// esc clears it.
	selected    map[string]bool
	filterInput textinput.Model
	answerInput textinput.Model
	cursor      int
	// offset is the first row drawn, so the cursor stays on screen in a
	// list longer than the terminal. Without it Home rendered all of them
	// and the terminal showed whichever came first, so `G` moved a cursor
	// nobody could see and the key hints sat below the fold.
	offset       int
	sort         homeSort
	filtering    bool
	answerActive bool
	// fleet requests every loaded project's threads instead of just the
	// launch root's, toggled by `w` without restarting.
	fleet bool
}

func newHomeState(th theme) homeState {
	return homeState{
		expanded:    map[string]bool{},
		selected:    map[string]bool{},
		filterInput: th.newInput("filter by name or directory"),
		answerInput: th.newInput("type an answer, or y/n/a"),
	}
}

// homeSort is the order Home lists threads in, cycled by `S`.
type homeSort int

// The recent order floats the threads waiting on an answer above the rest,
// because that list is a queue of work for the person reading it. Every
// other order is exactly what it says, so a sorted column reads straight
// down.
const (
	sortRecent homeSort = iota
	sortName
	sortDir
	sortTurns
	sortSpend
)

func (s homeSort) String() string {
	switch s {
	case sortName:
		return "name"
	case sortDir:
		return "dir"
	case sortTurns:
		return "turns"
	case sortSpend:
		return "spend"
	default:
		return "recent"
	}
}

func (s homeSort) next() homeSort {
	if s == sortSpend {
		return sortRecent
	}

	return s + 1
}

// homeRows filters and sorts the thread list.
func (m Model) homeRows() []api.ThreadInfo {
	rows := make([]api.ThreadInfo, 0, len(m.threads))

	q := strings.ToLower(strings.TrimSpace(m.home.filterInput.Value()))
	for i := range m.threads {
		if homeMatches(m.threads[i], q) {
			rows = append(rows, m.threads[i])
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if m.home.fleet {
			bi, bj := rootBase(rows[i].Root), rootBase(rows[j].Root)
			if bi != bj {
				return bi < bj
			}
		}

		return m.home.sort.less(rows[i], rows[j])
	})

	return rows
}

func (s homeSort) less(a, b api.ThreadInfo) bool {
	switch s {
	case sortName:
		return a.Name < b.Name
	case sortDir:
		return a.Dir < b.Dir
	case sortTurns:
		return a.Turns > b.Turns
	case sortSpend:
		return a.Spend > b.Spend
	default:
		if aw, bw := a.State == event.StateNeedsIn, b.State == event.StateNeedsIn; aw != bw {
			return aw
		}

		return a.LastEvent.After(b.LastEvent)
	}
}

// homeMatches is the filter, over everything the row shows. A `state:`
// term narrows by lifecycle position and matches no text, so `/state:failed`
// is the failures alone and a goal saying "failedEdit" stays out of it; the
// word after the colon is one the state column shows, and a word it does not
// show matches nothing rather than falling back to text, so a typo reads as
// an empty list instead of a silent match-all. Text without the prefix scans
// every field except the state word, since the prefix owns that narrowing.
func homeMatches(t api.ThreadInfo, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	text, state, hasState := cutStateTerm(q)
	if hasState && state == "" {
		return false
	}

	if hasState && t.State != state {
		return false
	}

	if text == "" {
		return true
	}

	for _, field := range []string{t.Name, t.Dir, rootBase(t.Root), t.Goal} {
		if strings.Contains(strings.ToLower(field), text) {
			return true
		}
	}

	return false
}

// cutStateTerm splits a query into its text and its `state:` term. The term
// may lead the query or follow the text, and whatever sits after its one
// word is text again. A `state:` with no word yet (a query still being
// typed) is no term at all.
func cutStateTerm(q string) (string, event.State, bool) {
	const prefix = "state:"

	i := strings.LastIndex(q, prefix)
	if i < 0 || i > 0 && q[i-1] != ' ' {
		return q, "", false
	}

	word, rest, _ := strings.Cut(q[i+len(prefix):], " ")
	text := strings.TrimSpace(q[:i] + " " + rest)
	if word == "" {
		return text, "", false
	}

	return text, stateWord(word), true
}

// stateWord resolves one typed word to a state, or "" when it names none.
// `needs-input` spells the one state whose wire name a person will not type.
func stateWord(w string) event.State {
	if w == "needs-input" {
		return event.StateNeedsIn
	}

	switch event.State(w) {
	case event.StateIdle, event.StateWorking, event.StateGating,
		event.StateNeedsIn, event.StateBlocked, event.StateFailed, event.StateDone:
		return event.State(w)
	}

	return ""
}

// rootBase names the group a fleet row renders under: the launch directory
// basename DESIGN.md's mockup groups by (`wavez/`, not the full path).
func rootBase(root string) string {
	if root == "" {
		return ""
	}

	return filepath.Base(root)
}

func (m Model) pendingFor(threadID string) *api.PendingInfo {
	for i := range m.pending {
		if m.pending[i].ThreadID == threadID {
			return &m.pending[i]
		}
	}

	return nil
}

func (m Model) updateHomeKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	if m.home.filtering {
		// Enter applies the filter: the query stays and the list keys come
		// back, so `*` can select what the filter narrowed to. Esc, in
		// popOrClose, is the cancel path and resets the query.
		if s == keyEnter {
			m.home.filtering = false
			m.home.filterInput.Blur()

			return m, nil
		}

		var cmd tea.Cmd
		m.home.filterInput, cmd = m.home.filterInput.Update(msg)

		return m, cmd
	}

	rows := m.homeRows()
	if m.home.answerActive && len(rows) > 0 {
		return m.homeAnswer(msg, s, rows[m.cappedCursor(len(rows))])
	}

	if cursor, moved := homeCursorMove(s, m.cappedCursor(len(rows)), len(rows)); moved {
		m.home.cursor = cursor

		return m, nil
	}

	mm, cmd := m.homeActionKey(msg, s, rows)
	mm.home.cursor = mm.cappedCursor(len(rows))

	return mm, cmd
}

// homeCursorMove handles the list-movement keys, split out of
// updateHomeKey to keep both under the complexity budget.
func homeCursorMove(s string, cursor, n int) (int, bool) {
	switch s {
	case keyJ, keyDown:
		return cursor + 1, true
	case "k", "up":
		return max(cursor-1, 0), true
	case "g":
		return 0, true
	case "G":
		return n - 1, true
	default:
		return cursor, false
	}
}

func (m Model) homeActionKey(msg tea.KeyPressMsg, s string, rows []api.ThreadInfo) (Model, tea.Cmd) {
	switch s {
	case "/":
		m.home.filtering = true

		return m, m.home.filterInput.Focus()
	case "S":
		m.home.sort = m.home.sort.next()
		m.home.cursor, m.home.offset = 0, 0

		return m, nil
	case "s":
		return m.openSchedule()
	case "w":
		return m.toggleScope()
	case "v":
		if len(rows) > 0 {
			return m.togglePeek(rows[m.cappedCursor(len(rows))].ID)
		}
	case keyEnter:
		if len(rows) > 0 {
			return m.openThread(rows[m.cappedCursor(len(rows))].ID)
		}
	case "y", "n", "a":
		// A pending prompt owns these letters while it is focused; with no
		// prompt on the row and none in the selection, `n` means new thread.
		if len(rows) > 0 {
			row := rows[m.cappedCursor(len(rows))]
			if m.pendingFor(row.ID) != nil || m.selectionPending() {
				return m.homeAnswer(msg, s, row)
			}
		}

		if s == "n" {
			return m.openNewThread("")
		}
	default:
		return m.homeSelectionKey(s, rows)
	}

	return m, nil
}

// homeSelectionKey is the selection-set keys, split out of homeActionKey
// the way homeCursorMove was split out of updateHomeKey, to keep both under
// the complexity budget.
func (m Model) homeSelectionKey(s string, rows []api.ThreadInfo) (Model, tea.Cmd) {
	switch s {
	case " ", "space":
		if len(rows) > 0 {
			return m.toggleSelected(rows[m.cappedCursor(len(rows))].ID), nil
		}
	case "*":
		return m.selectAll(rows), nil
	}

	return m, nil
}

// toggleSelected marks or unmarks one row for a bulk action.
func (m Model) toggleSelected(id string) Model {
	if m.home.selected[id] {
		delete(m.home.selected, id)

		return m
	}

	m.home.selected[id] = true

	return m
}

// selectAll marks every row the current filter shows, or clears the
// selection when every shown row is already marked, so the one key does
// both.
func (m Model) selectAll(rows []api.ThreadInfo) Model {
	all := true
	for i := range rows {
		if !m.home.selected[rows[i].ID] {
			all = false

			break
		}
	}

	if all {
		m.home.selected = map[string]bool{}

		return m
	}

	for i := range rows {
		m.home.selected[rows[i].ID] = true
	}

	return m
}

// selectionPending reports whether any selected row is waiting on an
// answer, which is what lets y/n/a act on a selection whose cursor row has
// nothing pending itself.
func (m Model) selectionPending() bool {
	for id := range m.home.selected {
		if m.pendingFor(id) != nil {
			return true
		}
	}

	return false
}

// toggleScope switches Home between the launch root and the whole fleet,
// re-requesting the list rather than restarting: a per-laptop daemon
// already serves every loaded project over one socket, so widening scope
// is just a different list request.
func (m Model) toggleScope() (Model, tea.Cmd) {
	m.home.fleet = !m.home.fleet
	m.home.cursor = 0

	if m.client == nil {
		return m, nil
	}

	return m, m.client.setScope(m.home.fleet)
}

// togglePeek expands or collapses a row. Events only flow for a subscribed
// thread, so the first peek at one subscribes to it: without that the row
// opened onto nothing until the thread had been visited.
func (m Model) togglePeek(id string) (Model, tea.Cmd) {
	m.home.expanded[id] = !m.home.expanded[id]

	if !m.home.expanded[id] || m.transcripts[id] != nil || m.client == nil {
		return m, nil
	}

	return m, m.client.subscribe(id)
}

func (m Model) cappedCursor(n int) int {
	if n == 0 {
		return 0
	}

	return min(max(m.home.cursor, 0), n-1)
}

func (m Model) openThread(id string) (Model, tea.Cmd) {
	m.thread.activeID = id
	m.push(screenThread)

	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.subscribe(id)
	}

	return m, cmd
}

func (m Model) homeAnswer(msg tea.KeyPressMsg, s string, row api.ThreadInfo) (Model, tea.Cmd) {
	// A selection answers every selected row that has one pending and
	// leaves the rest alone, so the cursor row's own prompt does not steal
	// the key when a bulk answer is what was asked for. A question's typed
	// answer keeps ownership of the keys while it is being written.
	if !m.home.answerActive && len(m.home.selected) > 0 {
		return m.homeAnswerSelection(s)
	}

	pending := m.pendingFor(row.ID)
	if pending == nil {
		m.home.answerActive = false

		return m, nil
	}

	if pending.Question {
		return m.homeAnswerQuestion(msg, s, *pending)
	}

	switch s {
	case "y":
		return m.sendAnswer(pending.ID, "", permission.Allow)
	case "n":
		return m.sendAnswer(pending.ID, "", permission.Deny)
	case "a":
		return m.sendAnswer(pending.ID, "", permission.AllowAlways)
	case keyEsc:
		m.home.answerActive = false
	}

	return m, nil
}

// homeAnswerSelection applies y/n/a to every selected row with a prompt
// pending and leaves the rest selected, so a selection of mixed rows keeps
// the ones the key did not touch. Questions need typed text, so a selection
// answers only the plain permission prompts; a question still takes its own
// answer inline.
func (m Model) homeAnswerSelection(s string) (Model, tea.Cmd) {
	var decision permission.Decision

	switch s {
	case "y":
		decision = permission.Allow
	case "n":
		decision = permission.Deny
	case "a":
		decision = permission.AllowAlways
	default:
		return m, nil
	}

	m.home.answerActive = false

	var cmds []tea.Cmd
	for id := range m.home.selected {
		pending := m.pendingFor(id)
		if pending == nil || pending.Question {
			continue
		}

		nm, cmd := m.sendAnswer(pending.ID, "", decision)
		m = nm
		delete(m.home.selected, id)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) homeAnswerQuestion(msg tea.KeyPressMsg, s string, pending api.PendingInfo) (Model, tea.Cmd) {
	if !m.home.answerActive {
		m.home.answerActive = true

		return m, m.home.answerInput.Focus()
	}

	if s == keyEnter {
		text := m.home.answerInput.Value()
		m.home.answerInput.Reset()

		return m.sendAnswer(pending.ID, text, permission.Allow)
	}

	var cmd tea.Cmd
	m.home.answerInput, cmd = m.home.answerInput.Update(msg)

	return m, cmd
}

func (m Model) sendAnswer(promptID, text string, decision permission.Decision) (Model, tea.Cmd) {
	m.home.answerActive = false
	m.dropPending(promptID)

	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.answer(promptID, text, decision)
	}

	return m, cmd
}

// dropPending removes an answered prompt from the local list so its row
// reads as answered on screen; the daemon's next pending push replaces the
// list wholesale, which reconciles anything the daemon disagreed with.
func (m *Model) dropPending(promptID string) {
	kept := m.pending[:0]
	for i := range m.pending {
		if m.pending[i].ID != promptID {
			kept = append(kept, m.pending[i])
		}
	}

	m.pending = kept
}

func (m Model) renderHome() string {
	rows := m.homeRows()
	cursor := m.cappedCursor(len(rows))

	title := fmt.Sprintf("wavez · %s · %s%s",
		m.homeScopeLabel(), needsInputBadge(m.needsInput(), m.ascii), diagStrip(m.diag))

	window := m.homeWindow(rows, cursor)

	visible := make([]api.ThreadInfo, 0, len(window))
	for _, at := range window {
		visible = append(visible, rows[at])
	}

	cols := m.homeColumns(visible)

	body := []string{m.th.fgMuted.Render(homeHeader(cols)), m.homeStatus(len(rows))}

	var lastRoot string

	for _, at := range window {
		t := &rows[at]

		if m.home.fleet {
			if rb := rootBase(t.Root); rb != lastRoot {
				body = append(body, m.th.fgEmphasis.Render(rb+"/"))
				lastRoot = rb
			}
		}

		body = append(body, m.renderHomeRow(*t, cols, at == cursor))

		if m.home.expanded[t.ID] {
			body = append(body, m.renderHomeExpanded(*t)...)
		}
	}

	if len(rows) == 0 {
		body = append(body, m.th.fgMuted.Render(m.homeEmpty()))
	}

	if m.home.filtering {
		body = append(body, m.th.fgMuted.Render("/ ")+m.home.filterInput.View())
	}

	footer := footerHints(homeHints(m.home.filtering), m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

func (m Model) needsInput() int {
	n := 0

	for i := range m.threads {
		if m.threads[i].State == event.StateNeedsIn {
			n++
		}
	}

	return n
}

func (m Model) homeEmpty() string {
	if m.home.filterInput.Value() != "" {
		return "no threads match · esc clears the filter"
	}

	return "no threads yet · press n to start one"
}

// homeStatus is the one line that says what the list is showing, because a
// filter that narrows 853 rows to 6 in silence is indistinguishable from a
// daemon that lost them.
func (m Model) homeStatus(shown int) string {
	parts := []string{fmt.Sprintf("%d of %d threads", shown, len(m.threads))}

	if q := strings.TrimSpace(m.home.filterInput.Value()); q != "" {
		parts = append(parts, "matching "+q)
	}

	if n := len(m.home.selected); n > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", n))
	}

	parts = append(parts, "by "+m.home.sort.String())

	return m.th.fgMuted.Render(strings.Join(parts, " · "))
}

// homeWindow is the slice of row indexes that fit, scrolled so the cursor
// is one of them.
func (m Model) homeWindow(rows []api.ThreadInfo, cursor int) []int {
	budget := m.homeListBudget()

	offset := min(m.home.offset, max(len(rows)-budget, 0))
	if cursor < offset {
		offset = cursor
	}

	if cursor >= offset+budget {
		offset = cursor - budget + 1
	}

	out := make([]int, 0, budget)
	for i := offset; i < len(rows) && len(out) < budget; i++ {
		out = append(out, i)
	}

	return out
}

// homeListBudget is how many rows fit once the two border lines, the column
// header, the status line, and any open filter input have taken theirs.
func (m Model) homeListBudget() int {
	chrome := frameRows + homeHeaderRows
	if m.home.filtering {
		chrome++
	}

	return max(m.height-chrome, 1)
}

// homeHeaderRows is the column header and the status line above the list.
const homeHeaderRows = 2

// homeCols is how wide each variable column is for the rows on screen. A
// name column fixed at 20 cut every row to "rename-the-exported…" while
// ninety columns sat empty, and a step column fixed at 27 spent them on
// "done" repeated down the screen.
type homeCols struct {
	name int
	step int
	dir  int
}

// homeColumns sizes the row against the rows actually visible: a column
// nothing on screen fills is not drawn, and the name takes what that frees.
// A single-project list agrees on its directory and a finished list has no
// live step, so reserving both leaves a screen of blanks.
func (m Model) homeColumns(visible []api.ThreadInfo) homeCols {
	c := homeCols{name: minNameColWidth}
	if m.home.fleet || distinctDirs(visible) > 1 {
		c.dir = dirColWidth
	}

	fixed := homeRowPrefix + c.dir + stateColWidth + turnsColWidth + ageColWidth + spendColWidth

	room := max(m.width-boxPad-fixed, minNameColWidth)
	if room <= preferredNameWidth+minStepColWidth || !anyStep(visible) {
		// Surplus goes to the right of the last column rather than into a
		// name column wider than any name in it, so the numbers stay beside
		// the names they belong to.
		c.name = min(room, max(widest(visible)+1, minNameColWidth))

		return c
	}

	c.name, c.step = preferredNameWidth, room-preferredNameWidth

	return c
}

func widest(rows []api.ThreadInfo) int {
	n := 0
	for i := range rows {
		n = max(n, lipgloss.Width(rows[i].Name))
	}

	return n
}

func distinctDirs(rows []api.ThreadInfo) int {
	seen := map[string]bool{}
	for i := range rows {
		seen[dirLabel(rows[i])] = true
	}

	return len(seen)
}

func anyStep(rows []api.ThreadInfo) bool {
	for i := range rows {
		if homeStep(rows[i]) != "" {
			return true
		}
	}

	return false
}

func homeHeader(c homeCols) string {
	head := fmt.Sprintf("%*s%-*s", homeRowPrefix, "", c.name, "thread")
	if c.step > 0 {
		head += fmt.Sprintf("%-*s", c.step, "step")
	}

	if c.dir > 0 {
		head += fmt.Sprintf("%-*s", c.dir, "dir")
	}

	return head + fmt.Sprintf("%-*s%*s %*s %*s",
		stateColWidth, "state",
		turnsColWidth-1, "turns", ageColWidth-1, "age", spendColWidth-1, "spend")
}

// Home's fixed column widths. The name column takes what is left, so the
// terminal getting wider makes the one variable-length field readable
// rather than making the gaps wider.
const (
	homeRowPrefix      = 4
	dirColWidth        = 16
	stateColWidth      = 9
	turnsColWidth      = 7
	ageColWidth        = 7
	spendColWidth      = 9
	minNameColWidth    = 12
	preferredNameWidth = 34
	minStepColWidth    = 14
	previewRows        = 3
	previewIndent      = 20
	goalPreviewLines   = 2
)

func (m Model) renderHomeRow(t api.ThreadInfo, c homeCols, current bool) string {
	prefix := "  "
	if t.Parent != "" {
		prefix = "└ "
	}

	line := fmt.Sprintf("%s%-*s", prefix, c.name, truncate(t.Name, c.name-1))
	if c.step > 0 {
		line += fmt.Sprintf("%-*s", c.step, truncate(homeStep(t), c.step-1))
	}

	if c.dir > 0 {
		line += fmt.Sprintf("%-*s", c.dir, truncate(dirLabel(t), c.dir-1))
	}

	line += fmt.Sprintf("%-*s%*d %*s %*s",
		stateColWidth, truncate(stateLabel(t.State), stateColWidth-1),
		turnsColWidth-1, t.Turns,
		ageColWidth-1, age(t.LastEvent, m.now()),
		spendColWidth-1, spend(t.Spend))

	// The gutter's two cells are the cursor `>` and the selection marker,
	// so a row can be both current and selected with neither hiding the
	// other.
	gutter := "  "
	if current {
		gutter = "> "
	}

	if m.home.selected[t.ID] {
		gutter = gutter[:1] + "*"
	}

	if current {
		return m.th.accent.Render(gutter + line)
	}

	return m.th.fgDefault.Render(gutter + line)
}

// homeStep is the live detail cell: what the thread is doing right now,
// blank where the step only repeats the state word this row already shows.
func homeStep(t api.ThreadInfo) string {
	if strings.EqualFold(t.Step, string(t.State)) {
		return ""
	}

	return oneLine(t.Step)
}

// dirLabel is where the thread works, relative to the project it belongs to,
// so a fleet list reads as "wavez internal/tui" rather than as two absolute
// paths that agree for their first forty characters.
func dirLabel(t api.ThreadInfo) string {
	if t.Dir == "" || t.Dir == t.Root {
		return rootBase(t.Root)
	}

	if rel, err := filepath.Rel(t.Root, t.Dir); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}

	return filepath.Base(t.Dir)
}

// homeScopeLabel is the title's location word: the repo name when Home is
// scoped to the launch root, or the common parent of every listed root
// in fleet scope, with the home directory shortened to "~".
func (m Model) homeScopeLabel() string {
	if !m.home.fleet {
		return rootBase(m.dir)
	}

	roots := make([]string, 0, len(m.threads)+1)
	roots = append(roots, m.dir)
	for i := range m.threads {
		if m.threads[i].Root != "" {
			roots = append(roots, m.threads[i].Root)
		}
	}

	return tildeHome(commonParent(roots))
}

func commonParent(paths []string) string {
	sep := string(filepath.Separator)
	parent := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		for !strings.HasPrefix(p+sep, parent+sep) {
			up := filepath.Dir(parent)
			if up == parent {
				return parent
			}
			parent = up
		}
	}

	return parent
}

func tildeHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}

	return "~" + strings.TrimPrefix(path, home)
}

func needsInputBadge(n int, ascii bool) string {
	if n == 0 {
		return ""
	}

	g := glyph(event.StateNeedsIn, ascii)

	return fmt.Sprintf("%s %d needs input · ", g, n)
}

// renderHomeExpanded is what a row opens onto: the goal first, then the
// last few events. A name is a slug of the prompt and a step is where the
// run has reached, so neither answers what the thread was for, and a list
// of replay lanes of one task is the same slug repeated until the goal is
// read.
func (m Model) renderHomeExpanded(t api.ThreadInfo) []string {
	var out []string

	pending := m.pendingFor(t.ID)
	if pending != nil {
		answers := "[y]es [n]o [a]lways"
		if pending.Question {
			answers = "> " + m.home.answerInput.View()
		}

		out = append(out, "  │  ▸ "+pending.Tool+"  "+pending.Action+"  "+answers)
		if pending.Reason != "" {
			out = append(out, m.th.fgMuted.Render(truncate("  │    "+pending.Reason, m.width-boxPad)))
		}
	}

	out = append(out, m.goalPreview(t)...)

	tr := m.transcripts[t.ID]
	if tr == nil {
		return out
	}

	for _, r := range tr.visible(previewRows, 0) {
		out = append(out, "  │  "+string(r.kind)+"  "+truncate(oneLine(r.text), m.width-previewIndent))
	}

	return out
}

func (m Model) goalPreview(t api.ThreadInfo) []string {
	if t.Goal == "" {
		return nil
	}

	lines := wrapLines(oneLine(t.Goal), m.width-previewIndent)
	if len(lines) > goalPreviewLines {
		lines = append(lines[:goalPreviewLines:goalPreviewLines], "")
		lines[goalPreviewLines-1] = truncate(lines[goalPreviewLines-1], m.width-previewIndent-1) + "…"
		lines = lines[:goalPreviewLines]
	}

	out := make([]string, 0, len(lines))
	for i, l := range lines {
		label := "goal  "
		if i > 0 {
			label = "      "
		}

		out = append(out, m.th.fgMuted.Render("  │  "+label+l))
	}

	return out
}

// oneLine folds a multi-line value onto one, because a row's preview is
// budgeted in lines and raw tool output carrying its own newlines wrote
// straight through the frame's right border.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func homeHints(filtering bool) []hint {
	if filtering {
		return []hint{{keyEnter, labelApply, ""}, {keyEsc, "cancel", ""}}
	}

	// Ordered by what a reader loses least when the footer is too narrow:
	// footerHints drops from the end, and the palette is the one entry that
	// is also reachable from help.
	return []hint{
		{keyEnter, labelOpen, ""},
		{"n", "new", "start a new thread"},
		{"v", "goal", "expand the row: goal, prompt, last rows"},
		{"/", "filter", "filter the thread list"},
		{"S", "sort", "change the list's sort order"},
		{"i", labelInbox, "open pending prompts"},
		{"s", "schedule", "open the schedule"},
		{"w", "scope", "show every project's threads"},
		{"D", "diag", "open the diagnostics panel"},
		{"q", labelQuit, ""},
		{"?", labelHelp, ""},
		{":", "palette", "open the command palette"},
		{"R", labelRoutines, "open the routines panel"},
	}
}
