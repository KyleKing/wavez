package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// searchState is Thread view's transcript search: the live input, the
// committed query, and which match the cursor sits on.
type searchState struct {
	query   string
	input   textinput.Model
	cursor  int
	editing bool
}

func newSearchState(th theme) searchState {
	return searchState{input: th.newInput("search the transcript")}
}

func (s searchState) visible() bool { return s.editing || s.query != "" }

// search returns the indices of rows matching query, case-insensitively,
// over the row's text and its tool name rather than its rendered label.
func (t *transcript) search(query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	var out []int

	for i := range t.rows {
		text := strings.ToLower(t.rows[i].text)
		tool := strings.ToLower(t.rows[i].tool)

		if strings.Contains(text, q) || strings.Contains(tool, q) {
			out = append(out, i)
		}
	}

	return out
}

func (m Model) searchMatches() []int {
	tr := m.transcripts[m.thread.activeID]
	if tr == nil || m.thread.search.query == "" {
		return nil
	}

	return tr.search(m.thread.search.query)
}

func (m Model) updateSearchKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	if s == keyEnter {
		return m.commitSearch(), nil
	}

	var cmd tea.Cmd
	m.thread.search.input, cmd = m.thread.search.input.Update(msg)

	return m, cmd
}

// threadSearchKey opens the search and steps its matches. An active query
// owns n/N ahead of the permission answers, because stepping a match when
// the user meant to deny costs nothing while the reverse denies a prompt
// they never read.
func (m Model) threadSearchKey(s string) (Model, tea.Cmd, bool) {
	switch s {
	case "/":
		if m.focus == focusInput {
			return m, nil, false
		}

		mm, cmd := m.openSearch()

		return mm, cmd, true
	case "n":
		if m.thread.search.query == "" {
			return m, nil, false
		}

		return m.stepMatch(1), nil, true
	case "N":
		if m.thread.search.query == "" {
			return m, nil, false
		}

		return m.stepMatch(-1), nil, true
	default:
		return m, nil, false
	}
}

func (m Model) openSearch() (Model, tea.Cmd) {
	m.thread.search.editing = true
	m.thread.search.input.SetValue(m.thread.search.query)

	return m, m.thread.search.input.Focus()
}

func (m *Model) clearSearch() {
	m.thread.search.editing = false
	m.thread.search.query = ""
	m.thread.search.cursor = 0
	m.thread.search.input.Blur()
	m.thread.search.input.Reset()
}

func (m Model) commitSearch() Model {
	m.thread.search.query = strings.TrimSpace(m.thread.search.input.Value())
	m.thread.search.editing = false
	m.thread.search.cursor = 0
	m.thread.search.input.Blur()

	return m.focusMatch()
}

func (m Model) stepMatch(delta int) Model {
	matches := m.searchMatches()
	if len(matches) == 0 {
		return m
	}

	m.thread.search.cursor = (m.thread.search.cursor + delta + len(matches)) % len(matches)

	return m.focusMatch()
}

// focusMatch scrolls the transcript only when the current match is off
// screen, so stepping between matches already in view does not jump. The
// match lands on the last visible line except near the top of the log,
// where the offset is capped so the window stays full.
func (m Model) focusMatch() Model {
	matches := m.searchMatches()
	if len(matches) == 0 {
		return m
	}

	idx := matches[min(m.thread.search.cursor, len(matches)-1)]
	total := len(m.transcripts[m.thread.activeID].rows)
	height := m.transcriptHeight()
	end := total - m.thread.scrollOffset

	if idx >= end-height && idx < end {
		return m
	}

	m.thread.scrollOffset = max(min(total-1-idx, total-height), 0)

	return m
}

func (m Model) searchLine(width int) string {
	if m.thread.search.editing {
		return m.th.fgMuted.Render("/ ") + m.thread.search.input.View()
	}

	matches := m.searchMatches()
	if len(matches) == 0 {
		return m.th.statusWarn.Render(truncate("/"+m.thread.search.query+"  no matches", width))
	}

	pos := fmt.Sprintf("/%s  %d/%d", m.thread.search.query, m.thread.search.cursor+1, len(matches))

	return m.th.fgMuted.Render(truncate(pos, width))
}

// highlightMatches wraps every case-insensitive occurrence of query in st.
// The offsets come from the lowercased copy, so a rune whose lowercase form
// is a different byte length would misalign them: such a row keeps its
// match without the highlight.
func highlightMatches(s, query string, st lipgloss.Style) string {
	if query == "" {
		return s
	}

	lower, lowerQuery := strings.ToLower(s), strings.ToLower(query)
	if len(lower) != len(s) {
		return s
	}

	var b strings.Builder

	for {
		i := strings.Index(lower, lowerQuery)
		if i < 0 {
			b.WriteString(s)

			return b.String()
		}

		b.WriteString(s[:i])
		b.WriteString(st.Render(s[i : i+len(lowerQuery)]))

		s, lower = s[i+len(lowerQuery):], lower[i+len(lowerQuery):]
	}
}
