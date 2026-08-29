package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// searchState is Thread view's transcript search: the live input, the
// committed query, which match the cursor sits on, and whether the query is
// matched fuzzily (every word, any order) or as one literal substring.
type searchState struct {
	query   string
	input   textinput.Model
	cursor  int
	editing bool
	fuzzy   bool
}

func newSearchState(th theme) searchState {
	return searchState{input: th.newInput("search the transcript")}
}

func (s searchState) visible() bool { return s.editing || s.query != "" }

// search returns the indices of rows in filter matching query,
// case-insensitively, over the row's text and its tool name rather than its
// rendered label.
func (t *transcript) search(query string, filter filterCategory) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	var out []int

	for i, r := range t.rows {
		if !matchesFilter(r, filter) {
			continue
		}

		text := strings.ToLower(r.text)
		tool := strings.ToLower(r.tool)

		if strings.Contains(text, q) || strings.Contains(tool, q) {
			out = append(out, i)
		}
	}

	return out
}

// fuzzySearch returns the indices of rows in filter whose text every word in
// query matches, in any order, using the same matcher the command palette
// filters its entries with.
func (t *transcript) fuzzySearch(query string, filter filterCategory) []int {
	if strings.TrimSpace(query) == "" {
		return nil
	}

	var out []int

	for i, r := range t.rows {
		if !matchesFilter(r, filter) {
			continue
		}

		if wordsMatch(rowText(r), query) {
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

	if m.thread.search.fuzzy {
		return tr.fuzzySearch(m.thread.search.query, m.thread.filter)
	}

	return tr.search(m.thread.search.query, m.thread.filter)
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
// they never read. `/` and `F` open the same overlay in the two matching
// modes rather than each owning a separate one, so there is one query, one
// cursor, and one highlight path between them; switching mode means closing
// and reopening the search, which also resets which match the cursor sits
// on rather than leaving it pointed at a match the other mode would not
// have found.
func (m Model) threadSearchKey(s string) (Model, tea.Cmd, bool) {
	switch s {
	case "/", "F":
		if m.focus == focusInput {
			return m, nil, false
		}

		mm, cmd := m.openSearch(s == "F")

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

func (m Model) openSearch(fuzzy bool) (Model, tea.Cmd) {
	m.thread.search.editing = true
	m.thread.search.fuzzy = fuzzy
	m.thread.search.input.SetValue(m.thread.search.query)

	return m, m.thread.search.input.Focus()
}

func (m *Model) clearSearch() {
	m.thread.search.editing = false
	m.thread.search.query = ""
	m.thread.search.cursor = 0
	m.thread.search.fuzzy = false
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
// match lands on its row's last visible line except near the top of the
// log, where the offset is capped so the window stays full. It works in
// rendered lines rather than rows, since an expanded or wrapped row can
// span more than one line and a row-counted offset would land on the wrong
// line whenever that happens.
func (m Model) focusMatch() Model {
	matches := m.searchMatches()
	if len(matches) == 0 {
		return m
	}

	tr := m.transcripts[m.thread.activeID]
	if tr == nil {
		return m
	}

	idx := matches[min(m.thread.search.cursor, len(matches)-1)]
	width := m.transcriptWidth()
	lineCount := tr.lineCount(width, m.thread.filter)

	height := 1
	if info, ok := m.activeThread(); ok {
		height = m.transcriptHeight(info)
	}

	lo, hi := rowLineSpan(tr, width, m.thread.filter, idx, lineCount)
	if lo >= hi {
		return m
	}

	end := lineCount - m.thread.scrollOffset
	start := max(end-height, 0)

	if lo >= start && hi <= end {
		return m
	}

	maxOffset := max(lineCount-height, 0)
	m.thread.scrollOffset = min(max(lineCount-hi, 0), maxOffset)

	return m
}

// searchGlyph names the active mode's marker: "/" is a literal substring,
// "~" is a fuzzy, any-order word match, matching how each mode is opened.
func (s searchState) searchGlyph() string {
	if s.fuzzy {
		return "~"
	}

	return "/"
}

func (m Model) searchLine(width int) string {
	glyph := m.thread.search.searchGlyph()

	if m.thread.search.editing {
		return m.th.fgMuted.Render(glyph+" ") + m.thread.search.input.View()
	}

	matches := m.searchMatches()
	if len(matches) == 0 {
		return m.th.statusWarn.Render(truncate(glyph+m.thread.search.query+"  no matches", width))
	}

	pos := fmt.Sprintf("%s%s  %d/%d", glyph, m.thread.search.query, m.thread.search.cursor+1, len(matches))

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

// highlightWords highlights every word of query separately, so a fuzzy
// query ("gofmt shell") highlights each word wherever it lands rather than
// only a row where the words appear together as typed. A single-word query
// (every literal `/` search) highlights exactly as highlightMatches alone
// would.
func highlightWords(s, query string, st lipgloss.Style) string {
	for _, w := range strings.Fields(query) {
		s = highlightMatches(s, w, st)
	}

	return s
}
