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
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tui"
)

func openThread(t *testing.T, m tui.Model, threads []api.ThreadInfo) tui.Model {
	t.Helper()

	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: threads})

	return apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestThread_VisibleWindowOnly(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 24)
	m = openThread(t, m, sampleThreads()[:1])

	const eventCount = 500

	msgs := make([]tea.Msg, 0, eventCount)
	for i := range eventCount {
		msgs = append(msgs, api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindTool, Text: "step " + strconv.Itoa(i), Seq: uint64(i),
		}})
	}

	m = apply(t, m, msgs...)
	out := m.View().Content

	assert.Contains(t, out, "step 499", "the most recent event must be visible")
	assert.NotContains(t, out, "step 0\n", "an event far outside the window must not render")
}

// An esc that falls through here pops the screen underneath while the goal
// keeps rendering, and `g` is a no-op off the thread screen, so nothing
// closes the overlay again.
func TestThread_GoalOverlayEscClosesWithoutPopping(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 24)
	m = openThread(t, m, sampleThreads()[:1])

	m = apply(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Contains(t, m.View().Content, "goal · fix-lock-timeout", "g opened the goal overlay")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	out := m.View().Content

	assert.NotContains(t, out, "goal · ", "esc closed the goal overlay")
	assert.Contains(t, out, "fix-lock-timeout", "the thread screen is still the one showing")
}

func TestThread_DiffPaneSummarizesChanges(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 24)
	m = openThread(t, m, sampleThreads()[:1])

	m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &event.Event{
		ThreadID: "t1", Kind: event.KindTool, Text: "edited lease.go",
		Changes: []tool.Change{{Path: "internal/lease/lease.go", Added: 6, Removed: 2}},
	}})

	out := m.View().Content
	assert.Contains(t, out, "internal/lease/lease.go  +6 -2")
}

func TestThread_DiffPaneStacksBelowNarrowWidth(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 80, 24)
	m = openThread(t, m, sampleThreads()[:1])

	out := m.View().Content
	assert.Contains(t, out, "(no changes yet)")
}

func TestThread_HeaderShowsModelAndContext(t *testing.T) {
	t.Parallel()

	threads := sampleThreads()[:1]
	threads[0].Model = "gemma4-12b"
	threads[0].Context = 3100
	threads[0].Window = 32000

	m := newSized(t, tui.Options{NoColor: true}, 100, 24)
	m = openThread(t, m, threads)

	out := m.View().Content
	assert.Contains(t, out, "gemma4-12b")
	assert.Contains(t, out, "3.1k/32.0k")
}

// searchRows is a transcript whose matches for "lease" are three rows: two
// by text (one of them upper-cased) and one only by its tool name.
func searchRows() []tea.Msg {
	texts := []struct{ tool, text string }{
		{"", "make the lease TTL configurable"},
		{"", "renamed the default"},
		{"lease-check", "ran the gate"},
		{"", "wrote LEASE.md"},
		{"", "gates green"},
	}

	msgs := make([]tea.Msg, 0, len(texts))
	for i, r := range texts {
		msgs = append(msgs, api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindTool, Tool: r.tool, Text: r.text, Seq: uint64(i),
		}})
	}

	return msgs
}

func searchFixture(t *testing.T, opts tui.Options, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, opts, width, height)
	m = openThread(t, m, sampleThreads()[:1])

	return apply(t, m, searchRows()...)
}

// typeQuery opens the search, types query, and commits it with Enter.
func typeQuery(t *testing.T, m tui.Model, query string) tui.Model {
	t.Helper()

	m = apply(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range query {
		m = apply(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	return apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestThreadSearch_StepsAndWrapsAcrossSizes(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 24}, {130, 32}} {
		t.Run(strconv.Itoa(size.w), func(t *testing.T) {
			t.Parallel()

			m := searchFixture(t, tui.Options{NoColor: true}, size.w, size.h)

			m = apply(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
			assert.Contains(t, m.View().Content, "[enter]apply [esc]cancel", "the input opens on /")

			m = typeQuery(t, apply(t, m, tea.KeyPressMsg{Code: tea.KeyEsc}), "lease")
			assert.Contains(t, m.View().Content, "/lease  1/3")

			for _, want := range []string{"2/3", "3/3", "1/3"} {
				m = apply(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
				assert.Contains(t, m.View().Content, want, "n steps forward and wraps")
			}

			m = apply(t, m, tea.KeyPressMsg{Code: 'N', Text: "N"})
			assert.Contains(t, m.View().Content, "3/3", "N wraps backwards")

			m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
			out := m.View().Content
			assert.NotContains(t, out, "3/3", "esc clears the search")
			assert.NotContains(t, out, "\x1b[7m", "esc clears the highlight")
		})
	}
}

// Reverse video is the highlight precisely because it survives NO_COLOR,
// where every color slot is empty.
func TestThreadSearch_HighlightsInReverseVideo(t *testing.T) {
	t.Parallel()

	for _, noColor := range []bool{true, false} {
		t.Run(strconv.FormatBool(noColor), func(t *testing.T) {
			t.Parallel()

			m := typeQuery(t, searchFixture(t, tui.Options{NoColor: noColor}, 100, 24), "lease")

			hits := linesContaining(m.View().Content, "\x1b[7m")
			assert.Len(t, hits, 3, "every matching row renders its hit in reverse video")

			for _, line := range hits {
				assert.Contains(t, strings.ToLower(line), "\x1b[7mlease", "the hit itself is what reverses")

				if noColor {
					assert.NotContains(t, line, "38;", "NO_COLOR must not emit a foreground color escape")
					assert.NotContains(t, line, "48;", "NO_COLOR must not emit a background color escape")
				}
			}
		})
	}
}

func linesContaining(out, want string) []string {
	var found []string

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, want) {
			found = append(found, line)
		}
	}

	return found
}

func TestThreadSearch_NoMatchSaysSo(t *testing.T) {
	t.Parallel()

	// "tool" is in every row's rendered label and in none of their text, so
	// a hit here would mean the search reads the label.
	for _, query := range []string{"nothing here", "tool"} {
		m := typeQuery(t, searchFixture(t, tui.Options{NoColor: true}, 100, 24), query)

		out := m.View().Content
		assert.Contains(t, out, "/"+query+"  no matches", query)
		assert.NotContains(t, out, "\x1b[7m", "a miss highlights nothing")
	}
}

// A match above the visible window scrolls into view rather than leaving
// the position counter pointing at a row nobody can see.
func TestThreadSearch_ScrollsAMatchOnScreen(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 24)
	m = openThread(t, m, sampleThreads()[:1])

	const eventCount = 200

	msgs := make([]tea.Msg, 0, eventCount)
	for i := range eventCount {
		text := "step " + strconv.Itoa(i)
		if i == 3 {
			text = "the needle"
		}

		msgs = append(msgs, api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindTool, Text: text, Seq: uint64(i),
		}})
	}

	m = apply(t, m, msgs...)
	require.NotContains(t, m.View().Content, "the needle")

	m = typeQuery(t, m, "needle")
	out := m.View().Content
	assert.Contains(t, out, "the \x1b[7mneedle", "the match scrolled into the window")
	assert.Contains(t, out, "step 2", "the rows around the match came with it")
	assert.Contains(t, out, "1/1")
}

func TestThread_SearchGoldenFrame(t *testing.T) {
	t.Parallel()

	m := typeQuery(t, searchFixture(t, tui.Options{Dir: "~/dev", NoColor: true}, 80, 24), "lease")

	goldenCompare(t, "thread_search_frame", m.View().Content)
}

// The confirmation names the work it would destroy, and Thread view
// advertises the key that reaches it.
func TestThread_UndoConfirmationShowsWhatItDiscards(t *testing.T) {
	t.Parallel()

	threads := sampleThreads()[:1]
	threads[0].Checkpoint = "op-abc"

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = openThread(t, m, threads)

	assert.Contains(t, m.View().Content, "[u]undo")

	m = apply(t, m, api.Reply{Kind: api.RepRestore, Restore: &api.Restore{
		ThreadID: "t1", Checkpoint: "op-abc",
		Summary: "internal/lease/lease.go | 8 ++++----\n1 files changed, 4 insertions(+), 4 deletions(-)\n",
	}})

	out := m.View().Content
	assert.Contains(t, out, "undo discards this uncommitted work")
	assert.Contains(t, out, "internal/lease/lease.go")
	assert.Contains(t, out, "1 files changed")
	assert.Contains(t, out, "[y]es")
}

// toolRows appends n single-line KindTool events named "row 0".."row n-1".
func toolRows(t *testing.T, m tui.Model, n int) tui.Model {
	t.Helper()

	msgs := make([]tea.Msg, 0, n)
	for i := range n {
		msgs = append(msgs, api.Reply{Kind: api.RepEvent, Event: &event.Event{
			ThreadID: "t1", Kind: event.KindTool, Text: "row " + strconv.Itoa(i), Seq: uint64(i),
		}})
	}

	return apply(t, m, msgs...)
}

// markedRow is the ">" prefix render puts on the cursor's row, immediately
// before its label; a folded, unselected row gets two spaces instead.
func markedRow(text string) string { return "> ▸ tool   " + text }

func TestThreadCursor_MovesAndClampsAtBothEnds(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{100, 30}, {80, 24}} {
		t.Run(strconv.Itoa(size.w), func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{NoColor: true}, size.w, size.h)
			m = openThread(t, m, sampleThreads()[:1])
			m = toolRows(t, m, 5)

			// The first j lands on the bottom row without moving further.
			m = apply(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
			assert.Contains(t, m.View().Content, markedRow("row 4"))

			// k steps up through every row and clamps at the first.
			for _, want := range []string{"row 3", "row 2", "row 1", "row 0", "row 0"} {
				m = apply(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
				assert.Contains(t, m.View().Content, markedRow(want))
			}

			// The down arrow steps back down and clamps at the last row.
			for _, want := range []string{"row 1", "row 2", "row 3", "row 4", "row 4"} {
				m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyDown, Text: "down"})
				assert.Contains(t, m.View().Content, markedRow(want))
			}

			assert.Len(t, linesContaining(m.View().Content, "> ▸"), 1, "only one row is ever marked")
		})
	}
}

func TestThreadCursor_EnterTogglesFoldOnCursorRow(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = openThread(t, m, sampleThreads()[:1])

	long := strings.Repeat("word ", 60) + "TAILMARK"
	m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &event.Event{
		ThreadID: "t1", Kind: event.KindTool, Text: long, Seq: 0,
	}})

	m = apply(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.NotContains(t, m.View().Content, "TAILMARK", "a tool row folds by default")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Contains(t, m.View().Content, "TAILMARK", "enter expands the row under the cursor")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.NotContains(t, m.View().Content, "TAILMARK", "enter again folds it back")
}

// A row wider than the whole panel must show from its own top rather than
// leaving the cursor's row scrolled past entirely.
func TestThreadCursor_ExpandedRowTallerThanPanelShowsFromTop(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 80, 24)
	m = openThread(t, m, sampleThreads()[:1])

	long := "HEADMARK " + strings.Repeat("word ", 200) + "TAILMARK"
	m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &event.Event{
		ThreadID: "t1", Kind: event.KindTool, Text: long, Seq: 0,
	}})

	m = apply(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	out := m.View().Content
	assert.Contains(t, out, "HEADMARK", "the row's own top stays visible rather than being scrolled past")
	assert.NotContains(t, out, "TAILMARK", "the row is taller than the panel so its tail runs off screen")
}

// A reader pinned to the bottom keeps seeing new events, and one who has
// scrolled up must not be yanked back down when more arrive.
func TestThread_TailFollowHoldsUntilTheReaderScrolls(t *testing.T) {
	t.Parallel()

	const rowCount = 30

	m := newSized(t, tui.Options{NoColor: true}, 100, 24)
	m = openThread(t, m, sampleThreads()[:1])
	m = toolRows(t, m, rowCount)

	require.Contains(t, m.View().Content, "row 29", "tail-follow shows the newest event by default")

	m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &event.Event{
		ThreadID: "t1", Kind: event.KindTool, Text: "row 30", Seq: rowCount,
	}})
	assert.Contains(t, m.View().Content, "row 30", "a reader at the bottom follows new events")

	// Scroll away from the bottom by walking the cursor up past the window.
	for range 20 {
		m = apply(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	}

	before := m.View().Content
	require.NotContains(t, before, "row 31", "sanity: row 31 does not exist yet")

	m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &event.Event{
		ThreadID: "t1", Kind: event.KindTool, Text: "row 31", Seq: rowCount + 1,
	}})
	after := m.View().Content

	assert.NotContains(t, after, "row 31", "a scrolled-up reader is not yanked to the new bottom")
	assert.Equal(t, before, after, "the reader's absolute window holds still while new events stream in")
}

// The header is the only standing surface that says which tier a thread is
// pinned to, so a pin that does not reach it is invisible between turns.
func TestThread_HeaderNamesThePinnedTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		override router.Choice
		want     string
	}{
		{name: "unpinned shows the local model", override: "", want: "qwen3:8b "},
		{name: "pinned fast", override: router.ChoiceFast, want: "qwen3:8b·pinned"},
		{name: "pinned balanced", override: router.ChoiceBalanced, want: "balanced·pinned"},
		{name: "pinned deep", override: router.ChoiceDeep, want: "deep·pinned"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{NoColor: true}, 120, 24)
			m = apply(t, m, api.Reply{Kind: api.RepDiag, Diag: &api.Diagnostics{LocalModel: "qwen3:8b"}})
			m = openThread(t, m, []api.ThreadInfo{
				{ID: "t1", Name: "fix-lock", Dir: "wavez", State: event.StateWorking, Override: tc.override},
			})

			assert.Contains(t, m.View().Content, tc.want)
		})
	}
}
