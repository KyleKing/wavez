package tui_test

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

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
