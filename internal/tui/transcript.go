package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// row is one displayed transcript line. Consecutive agent-text events
// coalesce into a single row (a streamed sentence otherwise arrives as
// dozens of one-token events), so row count is not event count.
type row struct {
	kind    event.Kind
	text    string
	tool    string
	changes []tool.Change
	seq     uint64
}

// transcript is a thread's virtualized, coalesced event view. Its visible
// method indexes directly into rows for the on-screen window; nothing joins
// the full history into one string.
type transcript struct {
	rows []row
}

// append adds e to the transcript, coalescing it into the previous row when
// both are agent text.
func (t *transcript) append(e event.Event) {
	if e.Kind == event.KindAgent && len(t.rows) > 0 {
		last := &t.rows[len(t.rows)-1]
		if last.kind == event.KindAgent {
			last.text += e.Text

			return
		}
	}

	t.rows = append(t.rows, row{kind: e.Kind, text: e.Text, tool: e.Tool, seq: e.Seq, changes: e.Changes})
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
// bottom by offset rows.
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

// renderRow formats one transcript row per its kind, matching Thread view's
// typed-row shape in DESIGN.md.
func renderRow(r row, width int, th theme, query string) string {
	label, style := rowLabel(r.kind, th)

	text := flatten(r.text)
	if r.tool != "" && r.kind == event.KindTool {
		text = r.tool + " " + text
	}

	text = truncate(text, width-len(label)-1)

	return style.Render(label) + " " + highlightMatches(text, query, th.searchHit)
}

// flatten collapses a row's text to one line. A transcript row is one line
// by construction, so a tool result carrying a whole file would otherwise
// print its newlines straight through the frame and destroy the layout.
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
