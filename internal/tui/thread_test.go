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
