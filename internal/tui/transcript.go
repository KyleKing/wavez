package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/link"
	"github.com/kyleking/wavez/internal/tool"
)

// row is one displayed transcript line. Consecutive agent-text events
// coalesce into a single row (a streamed sentence otherwise arrives as
// dozens of one-token events), so row count is not event count.
type row struct {
	kind     event.Kind
	text     string
	tool     string
	role     event.Role
	changes  []tool.Change
	seq      uint64
	expanded bool
	toggled  bool
}

// transcript is a thread's virtualized, coalesced event view. Its visible
// method indexes directly into rows for the on-screen window; nothing joins
// the full history into one string.
type transcript struct {
	rows []row
}

// append adds e to the transcript, coalescing it into the previous row when
// both are agent text. A KindState or KindAgent event with no Text renders
// nothing on its own, so it is dropped rather than appended as a blank row;
// the one exception is a KindAgent event carrying a Role, which is a marker
// that types the preceding agent row instead of becoming a row itself.
func (t *transcript) append(e event.Event) {
	if e.Kind == event.KindAgent && e.Role != "" && e.Text == "" {
		t.applyRole(e.Role)

		return
	}

	if e.Kind == event.KindState && e.Text == "" {
		return
	}

	if e.Kind == event.KindAgent && e.Text == "" {
		return
	}

	if e.Kind == event.KindAgent && len(t.rows) > 0 {
		last := &t.rows[len(t.rows)-1]
		if last.kind == event.KindAgent && last.role == "" {
			last.text += e.Text

			return
		}
	}

	t.rows = append(t.rows, row{
		kind: e.Kind, text: e.Text, tool: e.Tool, seq: e.Seq, changes: e.Changes,
		expanded: defaultExpanded(e.Kind, e.Role),
	})
}

// applyRole types the last row with role, when it is an agent row and the
// reader has not already chosen a fold state for it.
func (t *transcript) applyRole(role event.Role) {
	if len(t.rows) == 0 {
		return
	}

	last := &t.rows[len(t.rows)-1]
	if last.kind != event.KindAgent {
		return
	}

	last.role = role
	if !last.toggled {
		last.expanded = defaultExpanded(last.kind, last.role)
	}
}

// defaultExpanded is the fold state a row starts in before the reader
// toggles it: an answer is unfolded so it can be read in full, everything
// else (including a note, and an agent row awaiting its role) folds to one
// line per DESIGN.md's Thread view.
func defaultExpanded(k event.Kind, r event.Role) bool {
	return k == event.KindAgent && r == event.RoleAnswer
}

// toolShell is internal/tools' Shell.Name(), the one tool name the kind
// filter treats as a shell rather than an edit.
const toolShell = "shell"

// filterCategory groups a row for Thread view's kind filter and its
// "what was done" summary. It layers over kind, tool, and role rather than
// mapping straight from event.Kind: an edit and a shell are both KindTool
// distinguished by row.tool, and an answer is KindAgent with RoleAnswer.
// Its zero value, catNone, doubles as "no category" (row.category's zero
// value) and "no filter applied" (matchesFilter's zero value); this is safe
// because a row's category is never compared against catNone to mean "show
// it".
type filterCategory string

// The categories the kind filter and the summary group rows by, in
// DESIGN.md's Thread view order.
const (
	catNone       filterCategory = ""
	catEdit       filterCategory = "edits"
	catShell      filterCategory = "shells"
	catGate       filterCategory = "gates"
	catPermission filterCategory = "permissions"
	catAnswer     filterCategory = "answers"
)

// filterCategories is catNone (meaning "all") followed by every named
// category in display order, what cycleKindFilter steps through.
var filterCategories = []filterCategory{catNone, catEdit, catShell, catGate, catPermission, catAnswer}

// category reports which named category r belongs to, or catNone when it
// belongs to none of them. A tool row is an edit when it produced file
// changes rather than by name, since read/search/context/question/hypothesis
// tool calls never carry Changes and a future read-only tool needs no entry
// here to stay out of the edits group.
func (r row) category() filterCategory {
	switch {
	case r.kind == event.KindTool && len(r.changes) > 0:
		return catEdit
	case r.kind == event.KindTool && r.tool == toolShell:
		return catShell
	case r.kind == event.KindGate:
		return catGate
	case r.kind == event.KindPermission:
		return catPermission
	case r.kind == event.KindAgent && r.role == event.RoleAnswer:
		return catAnswer
	default:
		return catNone
	}
}

// matchesFilter reports whether r passes filter, where catNone keeps every
// row.
func matchesFilter(r row, filter filterCategory) bool {
	return filter == catNone || r.category() == filter
}

// nextFilterCategory steps forward through filterCategories, wrapping from
// the last named category back to catNone ("all"). A cur not found in the
// list (impossible in practice, since threadState only ever holds a value
// from this list) restarts at catNone rather than panicking.
func nextFilterCategory(cur filterCategory) filterCategory {
	for i, c := range filterCategories {
		if c == cur {
			return filterCategories[(i+1)%len(filterCategories)]
		}
	}

	return filterCategories[0]
}

// visibleRows reports every row index filter keeps, ascending. The row
// cursor and the "what was done" summary walk this instead of t.rows
// directly, so a row's category and its filter match are decided in the
// one place, row.category.
func (t *transcript) visibleRows(filter filterCategory) []int {
	if filter == catNone {
		idx := make([]int, len(t.rows))
		for i := range t.rows {
			idx[i] = i
		}

		return idx
	}

	var out []int

	for i, r := range t.rows {
		if r.category() == filter {
			out = append(out, i)
		}
	}

	return out
}

// count reports how many rows the transcript holds.
func (t *transcript) count() int {
	return len(t.rows)
}

// toggle flips row i's folded state, reporting whether i named a row.
func (t *transcript) toggle(i int) bool {
	if i < 0 || i >= len(t.rows) {
		return false
	}

	t.rows[i].expanded = !t.rows[i].expanded
	t.rows[i].toggled = true

	return true
}

// changeStats aggregates every row's file changes by path, in first-seen
// order, for the diff pane's change summary.
func (t *transcript) changeStats() ([]string, map[string][2]int) { //nolint:gocritic // named returns are forbidden
	stats := map[string][2]int{}

	var paths []string

	for _, r := range t.rows {
		for _, c := range r.changes {
			if _, seen := stats[c.Path]; !seen {
				paths = append(paths, c.Path)
			}

			s := stats[c.Path]
			s[0] += c.Added
			s[1] += c.Removed
			stats[c.Path] = s
		}
	}

	return paths, stats
}

// visible returns the window of rows that fits height, scrolled up from the
// bottom by offset rows. It predates rows spanning more than one rendered
// line and stays row-counted for Home's peek preview; render is the
// line-accurate equivalent for Thread view's transcript panel.
func (t *transcript) visible(height, offset int) []row {
	if height < 0 {
		height = 0
	}

	end := len(t.rows) - offset
	if end > len(t.rows) {
		end = len(t.rows)
	}
	if end < 0 {
		end = 0
	}

	start := end - height
	if start < 0 {
		start = 0
	}

	return t.rows[start:end]
}

// renderOpts configures transcript.render. Offset counts rendered lines
// scrolled up from the bottom, not rows, since an expanded row can span
// several lines and a row-counted offset would jump the window by an
// unpredictable amount as rows fold and unfold.
type renderOpts struct {
	query  string
	filter filterCategory
	links  link.Table
	theme  theme
	width  int
	height int
	offset int
	cursor int
}

// render returns at most o.height lines, windowed from the bottom by
// o.offset lines, with the cursor row marked.
func (t *transcript) render(o renderOpts) []string {
	lines, _ := t.renderLines(o.width, o.theme, o.query, o.cursor, o.filter, o.links)

	height := max(o.height, 0)

	maxOffset := max(len(lines)-height, 0)
	offset := min(max(o.offset, 0), maxOffset)

	end := len(lines) - offset
	start := max(end-height, 0)

	return lines[start:end]
}

// lineCount reports the transcript's total rendered height at width under
// filter, which is what bounds a scroll offset once a row can occupy more
// than one line.
func (t *transcript) lineCount(width int, filter filterCategory) int {
	lines, _ := t.renderLines(width, theme{}, "", -1, filter, link.Table{})

	return len(lines)
}

// rowAtLine maps a rendered line index to its row index under filter.
func (t *transcript) rowAtLine(width int, filter filterCategory, line int) int {
	_, rowOf := t.renderLines(width, theme{}, "", -1, filter, link.Table{})
	if line < 0 || line >= len(rowOf) {
		return -1
	}

	return rowOf[line]
}

// renderLines renders every row filter keeps, in order: folded rows to one
// line and expanded rows wrapped over as many lines as their text needs. It
// returns the flattened line list alongside a parallel slice naming each
// line's source row index.
//
//nolint:gocritic // named returns are forbidden
func (t *transcript) renderLines(
	width int, th theme, query string, cursor int, filter filterCategory, links link.Table,
) ([]string, []int) {
	var lines []string

	var rowOf []int

	for i, r := range t.rows {
		if !matchesFilter(r, filter) {
			continue
		}

		rl := renderRowLines(r, width, th, query, i == cursor, links)
		lines = append(lines, rl...)

		for range rl {
			rowOf = append(rowOf, i)
		}
	}

	return lines, rowOf
}

// renderRowLines renders row r as one or more display lines: folded (the
// default for everything but an answer) truncates to one line with an
// ellipsis affordance when content was cut, so a folded row that has more
// to read looks different from one that does not; expanded wraps the full
// text at width instead of cutting it.
func renderRowLines(r row, width int, th theme, query string, marked bool, links link.Table) []string {
	label, style := rowLabel(r.kind, th)

	switch r.role {
	case event.RoleAnswer:
		style = th.fgEmphasis
	case event.RoleNote:
		style = th.fgMuted
	}

	prefix := "  "
	if marked {
		prefix = "> "
	}

	indent := lipgloss.Width(prefix) + lipgloss.Width(label) + 1
	pad := strings.Repeat(" ", max(indent, 0))

	text := rowText(r)
	if r.kind == event.KindUser || r.kind == event.KindAgent {
		text = links.Linkify(text)
	}

	if !r.expanded {
		line := truncate(text, width-indent)

		return []string{prefix + style.Render(label) + " " + highlightWords(line, query, th.searchHit)}
	}

	wrapped := strings.Split(lipgloss.Wrap(text, max(width-indent, 1), ""), "\n")

	out := make([]string, 0, len(wrapped))

	for i, w := range wrapped {
		highlighted := highlightWords(w, query, th.searchHit)
		if i == 0 {
			out = append(out, prefix+style.Render(label)+" "+highlighted)

			continue
		}

		out = append(out, pad+highlighted)
	}

	return out
}

// rowText is a row's normalized, single-line content: a tool row is
// prefixed by its tool name to match the label, per DESIGN.md's mock
// ("▸ tool  ran gofmt").
func rowText(r row) string {
	text := flatten(r.text)
	if r.tool != "" && r.kind == event.KindTool {
		text = r.tool + " " + text
	}

	return text
}

// flatten collapses a row's text to one line. A transcript row's fold state
// is what controls how many lines it spans, so text destined for either a
// folded or an expanded row is normalized the same way first: a tool result
// carrying a whole file would otherwise print its newlines straight through
// the frame and destroy the layout.
func flatten(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}

	return strings.Join(strings.Fields(s), " ")
}

func rowLabel(k event.Kind, th theme) (string, lipgloss.Style) {
	switch k {
	case event.KindUser:
		return "▸ user  ", th.fgDefault
	case event.KindAgent:
		return "▸ agent ", th.fgDefault
	case event.KindTool:
		return "▸ tool  ", th.statusInfo
	case event.KindGate:
		return "▸ gate  ", th.statusOK
	case event.KindPermission:
		return "▸ perm  ", th.statusWarn
	case event.KindError:
		return "▸ error ", th.statusErr
	case event.KindCycle:
		return "▸ cycle ", th.statusInfo
	case event.KindHypothesis:
		return "  hypoth", th.fgMuted
	case event.KindLedger:
		return "  ledger", th.fgMuted
	case event.KindUsage:
		return "  usage ", th.fgMuted
	case event.KindState:
		return "  state ", th.fgMuted
	default:
		return "▸ " + string(k), th.fgDefault
	}
}
