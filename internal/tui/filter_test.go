package tui_test

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tui"
)

// categorizedRows is one row of each kind filter category, plus one row
// ("small talk") that belongs to none of them, in a fixed order: edit,
// shell, gate, permission, note, answer.
func categorizedRows() []tea.Msg {
	return []tea.Msg{
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindTool, Tool: "write", Text: "wrote lease.go", Seq: 0,
			Changes: []tool.Change{{Path: "internal/lease/lease.go", Added: 1}},
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindTool, Tool: "shell", Text: "ran go test", Seq: 1,
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindGate, Text: "format passed", Seq: 2,
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindPermission, Tool: "shell", Text: "allow rm -rf tmp?", Seq: 3,
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindAgent, Text: "small talk", Seq: 4,
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindAgent, Role: event.RoleNote, Seq: 5,
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindAgent, Text: "the answer", Seq: 6,
		}},
		api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindAgent, Role: event.RoleAnswer, Seq: 7,
		}},
	}
}

func filterFixture(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, tui.Options{NoColor: true}, width, height)
	m = openThread(t, m, sampleThreads()[:1])

	return apply(t, m, categorizedRows()...)
}

func pressC(t *testing.T, m tui.Model) tui.Model {
	t.Helper()

	return apply(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
}

// TestThreadFilter_EachCategoryKeepsOnlyItsOwnRows cycles the kind filter
// through every named category and checks the transcript panel shows only
// that category's row and hides the rest.
func TestThreadFilter_EachCategoryKeepsOnlyItsOwnRows(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{100, 30}, {80, 24}} {
		t.Run(strconv.Itoa(size.w), func(t *testing.T) {
			t.Parallel()

			cases := []struct {
				want    string
				exclude []string
			}{
				{"wrote lease.go", []string{"ran go test", "format passed", "allow rm -rf tmp", "the answer"}},
				{"ran go test", []string{"wrote lease.go", "format passed", "allow rm -rf tmp", "the answer"}},
				{"format passed", []string{"wrote lease.go", "ran go test", "allow rm -rf tmp", "the answer"}},
				{"allow rm -rf tmp", []string{"wrote lease.go", "ran go test", "format passed", "the answer"}},
				{"the answer", []string{"wrote lease.go", "ran go test", "format passed", "allow rm -rf tmp"}},
			}

			m := filterFixture(t, size.w, size.h)

			for _, tc := range cases {
				m = pressC(t, m)
				out := m.View().Content

				assert.Contains(t, out, tc.want)

				for _, x := range tc.exclude {
					assert.NotContains(t, out, x, "filtered to %q, must not still show %q", tc.want, x)
				}
			}

			// One more cycle returns to "all": every row is back.
			m = pressC(t, m)
			out := m.View().Content
			for _, tc := range cases {
				assert.Contains(t, out, tc.want)
			}
		})
	}
}

// TestThreadFilter_CursorCannotLandOnHiddenRow moves the cursor with j/k
// while a filter is active and checks the marked row is always one the
// filter kept.
func TestThreadFilter_CursorCannotLandOnHiddenRow(t *testing.T) {
	t.Parallel()

	m := filterFixture(t, 100, 30)
	m = pressC(t, m) // edits

	for range 10 {
		m = apply(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
		out := m.View().Content

		if !assert.Contains(t, out, "> ▸ tool   write wrote lease.go") {
			t.Fatalf("cursor left the only visible row:\n%s", out)
		}
	}
}

// TestThreadFilter_EscClearsAndRestoresEveryRow checks Esc clears an active
// filter (per Thread view's existing ladder: search first, then filter,
// then leave the screen) rather than leaving the screen immediately. It
// renders wide so every footer hint fits, since footerHints truncates well
// before the filter hint at a realistic terminal width.
func TestThreadFilter_EscClearsAndRestoresEveryRow(t *testing.T) {
	t.Parallel()

	m := filterFixture(t, 220, 30)
	m = pressC(t, m) // edits
	require.NotContains(t, m.View().Content, "ran go test")
	require.Contains(t, m.View().Content, "[esc]clear filter")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	out := m.View().Content

	assert.Contains(t, out, "ran go test", "esc restored the hidden rows")
	assert.Contains(t, out, "format passed")
	assert.Contains(t, out, "[esc]home", "esc's hint reverts once the filter is clear")

	// A screen still on the stack: esc must not have popped Thread view.
	assert.Contains(t, out, "calcipy · fix-lock-timeout")
}

// TestThreadFuzzySearch_FindsAcrossWordOrderSubstringWouldMiss opens the
// fuzzy search with F and checks it finds a row whose words appear out of
// order relative to the query, which the plain `/` substring search does
// not.
func TestThreadFuzzySearch_FindsAcrossWordOrderSubstringWouldMiss(t *testing.T) {
	t.Parallel()

	m := filterFixture(t, 100, 30)

	m = apply(t, m, tea.KeyPressMsg{Code: 'F', Text: "F"})
	for _, r := range "tmp allow" {
		m = apply(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out := m.View().Content

	assert.Contains(t, out, "~tmp allow  1/1", "fuzzy search matched the permission row despite the word order")
}

// TestThreadSubstringSearch_StillWorksUnchanged is a regression check that
// adding fuzzy search left plain `/` search doing exactly what it did
// before: a literal, ordered substring match.
func TestThreadSubstringSearch_StillWorksUnchanged(t *testing.T) {
	t.Parallel()

	m := filterFixture(t, 100, 30)
	m = typeQuery(t, m, "allow rm")

	assert.Contains(t, m.View().Content, "/allow rm  1/1")
}

// TestThreadSummary_GroupsRowsByOperationType opens the "what was done"
// summary and checks every category's row lands under its own heading.
func TestThreadSummary_GroupsRowsByOperationType(t *testing.T) {
	t.Parallel()

	m := filterFixture(t, 100, 30)
	m = apply(t, m, tea.KeyPressMsg{Code: 's', Text: "s"})

	out := m.View().Content

	assert.Contains(t, out, "EDITS")
	assert.Contains(t, out, "SHELLS")
	assert.Contains(t, out, "GATES")
	assert.Contains(t, out, "PERMISSIONS")
	assert.Contains(t, out, "ANSWERS")
	assert.Contains(t, out, "wrote lease.go")
	assert.Contains(t, out, "ran go test")
	assert.Contains(t, out, "format passed")
	assert.Contains(t, out, "allow rm -rf tmp")
	assert.Contains(t, out, "the answer")
	assert.NotContains(t, out, "small talk", "a row outside the named categories is left out of the summary")

	edits := strings.Index(out, "EDITS")
	shells := strings.Index(out, "SHELLS")
	wrote := strings.Index(out, "wrote lease.go")
	require.True(t, edits >= 0 && shells >= 0 && wrote >= 0)
	assert.True(t, edits < wrote && wrote < shells, "the edit row sits under the EDITS heading, above SHELLS")
}

// TestThreadFilter_SaysWhichFilterHidesTheRows covers a filter that empties
// the transcript: the header names it and the body explains the emptiness,
// since a footer hint is dropped as the terminal narrows and a blank panel
// otherwise reads as a thread that did nothing.
func TestThreadFilter_SaysWhichFilterHidesTheRows(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 120} {
		t.Run("width "+strconv.Itoa(width), func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{NoColor: true}, width, 24)
			m = openThread(t, m, sampleThreads()[:1])
			m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &event.Event{
				ThreadID: "t1", Kind: event.KindGate, Text: "format passed", Seq: 0,
			}})

			got := m.View().Content
			assert.NotContains(t, got, "only", "an unfiltered thread names no filter")

			m = pressC(t, m)
			got = m.View().Content

			assert.Contains(t, got, "edits only", "the header names the active filter")
			assert.Contains(t, got, "no edits in this thread", "the body explains the empty panel")
			assert.NotContains(t, got, "format passed", "the gate row is hidden")
		})
	}
}
