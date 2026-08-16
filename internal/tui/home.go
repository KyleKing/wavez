package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

// homeState is Home's list cursor, filter, and per-row expansion.
type homeState struct {
	expanded     map[string]bool
	filterInput  textinput.Model
	answerInput  textinput.Model
	cursor       int
	filtering    bool
	answerActive bool
}

func newHomeState() homeState {
	filter := textinput.New()
	filter.Placeholder = "filter by name or directory"
	filter.Prompt = ""

	answer := textinput.New()
	answer.Placeholder = "type an answer, or y/n/a"
	answer.Prompt = ""

	return homeState{
		expanded:    map[string]bool{},
		filterInput: filter,
		answerInput: answer,
	}
}

// homeRows filters and sorts threads: needs-input first, then most recent.
func (m Model) homeRows() []api.ThreadInfo {
	rows := make([]api.ThreadInfo, len(m.threads))
	copy(rows, m.threads)

	q := strings.ToLower(strings.TrimSpace(m.home.filterInput.Value()))
	if q != "" {
		filtered := rows[:0]

		for i := range rows {
			if strings.Contains(strings.ToLower(rows[i].Name), q) || strings.Contains(strings.ToLower(rows[i].Dir), q) {
				filtered = append(filtered, rows[i])
			}
		}

		rows = filtered
	}

	sort.SliceStable(rows, func(i, j int) bool {
		iWait := rows[i].State == event.StateNeedsIn
		jWait := rows[j].State == event.StateNeedsIn
		if iWait != jWait {
			return iWait
		}

		return rows[i].LastEvent.After(rows[j].LastEvent)
	})

	return rows
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
	case "v":
		if len(rows) > 0 {
			id := rows[m.cappedCursor(len(rows))].ID
			m.home.expanded[id] = !m.home.expanded[id]
		}
	case keyEnter:
		if len(rows) > 0 {
			return m.openThread(rows[m.cappedCursor(len(rows))].ID)
		}
	case "y", "n", "a":
		if len(rows) > 0 {
			row := rows[m.cappedCursor(len(rows))]
			if m.pendingFor(row.ID) != nil {
				return m.homeAnswer(msg, s, row)
			}
		}
	}

	return m, nil
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

	var cmd tea.Cmd
	if m.client != nil {
		cmd = m.client.answer(promptID, text, decision)
	}

	return m, cmd
}

func (m Model) renderHome() string {
	rows := m.homeRows()

	needsInput := 0
	for i := range m.threads {
		if m.threads[i].State == event.StateNeedsIn {
			needsInput++
		}
	}

	title := fmt.Sprintf("wavez · %s · %d threads · %s%s",
		m.dir, len(m.threads), needsInputBadge(needsInput, m.ascii), diagStrip(m.diag))

	var body []string
	body = append(body, m.th.fgMuted.Render(fmt.Sprintf("%-3s%-22s%-28s%-7s%s", "", "thread", "step", "age", "spend")))

	var lastDir string

	for i := range rows {
		t := &rows[i]

		if t.Dir != lastDir {
			body = append(body, m.th.fgEmphasis.Render(t.Dir+"/"))
			lastDir = t.Dir
		}

		body = append(body, m.renderHomeRow(*t, i == m.cappedCursor(len(rows))))

		if m.home.expanded[t.ID] {
			body = append(body, m.renderHomeExpanded(*t)...)
		}
	}

	if len(rows) == 0 {
		body = append(body, m.th.fgMuted.Render("no threads match"))
	}

	if m.home.filtering {
		body = append(body, m.th.fgMuted.Render("/ ")+m.home.filterInput.View())
	}

	footer := footerHints(homeHints(m.home.filtering), m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

func needsInputBadge(n int, ascii bool) string {
	if n == 0 {
		return ""
	}

	g := glyph(event.StateNeedsIn, ascii)

	return fmt.Sprintf("%s %d needs input · ", g, n)
}

// Column widths for Home's thread rows, matching DESIGN.md's mockup.
const (
	nameColWidth  = 20
	stepColWidth  = 27
	previewRows   = 3
	previewIndent = 20
)

func (m Model) renderHomeRow(t api.ThreadInfo, selected bool) string {
	g := glyph(t.State, m.ascii)
	prefix := "  "

	if t.Parent != "" {
		prefix = "└ "
	}

	line := fmt.Sprintf("%s%s %-20s %-27s %-6s %s", prefix, g,
		truncate(t.Name, nameColWidth), truncate(t.Step, stepColWidth), age(t.LastEvent, m.now()), spend(t.Spend))

	if selected {
		return m.th.accent.Render("> " + line)
	}

	return m.th.fgDefault.Render("  " + line)
}

func (m Model) renderHomeExpanded(t api.ThreadInfo) []string {
	tr := m.transcripts[t.ID]

	var out []string

	pending := m.pendingFor(t.ID)
	if pending != nil {
		answers := "[y]es [n]o [a]lways"
		if pending.Question {
			answers = "> " + m.home.answerInput.View()
		}

		out = append(out, "  │  ▸ "+pending.Tool+"  "+pending.Action+"  "+answers)
	}

	if tr == nil {
		return out
	}

	rows := tr.visible(previewRows, 0)
	for _, r := range rows {
		out = append(out, "  │  ▸ "+string(r.kind)+"  "+truncate(r.text, m.width-previewIndent))
	}

	return out
}

func homeHints(filtering bool) []hint {
	if filtering {
		return []hint{{keyEnter, "apply"}, {keyEsc, "cancel"}}
	}

	return []hint{
		{keyEnter, labelOpen},
		{"v", "peek"},
		{"i", labelInbox},
		{"D", "diag"},
		{"/", "filter"},
		{":", "palette"},
		{"q", labelQuit},
		{"?", labelHelp},
	}
}
