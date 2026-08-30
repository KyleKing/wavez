package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

// pressKeys drives one Model through a run of single-character keys.
func pressKeys(t *testing.T, m Model, keys string) Model {
	t.Helper()

	for _, r := range keys {
		got, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})

		next, ok := got.(Model)
		require.True(t, ok)

		m = next
	}

	return m
}

func archiveModel(t *testing.T, fc *fakeClient) Model {
	t.Helper()

	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	got, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sized, ok := got.(Model)
	require.True(t, ok)

	sized.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", Dir: "calcipy", State: event.StateDone},
		{ID: "t2", Name: "flaky-ci", Dir: "calcipy", State: event.StateFailed},
	}})

	return sized
}

func TestHomeArchive_MovesTheSelectionAndRelistsIt(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := pressKeys(t, archiveModel(t, fc), "*A")

	require.Len(t, fc.archives, 2)
	assert.Equal(t, []archiveCall{
		{threadID: "t1", archived: true}, {threadID: "t2", archived: true},
	}, fc.archives)
	assert.Equal(t, []bool{false}, fc.archiveView, "the working list is re-read after the rows leave it")
	assert.Empty(t, m.home.selected, "the selection goes with the rows that moved")
}

// With nothing selected the key acts on the row under the cursor, so
// archiving one thread is not a two-key ceremony.
func TestHomeArchive_FallsBackToTheCursorRow(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	pressKeys(t, archiveModel(t, fc), "A")

	require.Len(t, fc.archives, 1)
	assert.Equal(t, archiveCall{threadID: "t1", archived: true}, fc.archives[0])
}

// In the archived view the same key restores, because it acts on what is on
// screen rather than on a fixed direction.
func TestHomeArchive_RestoresFromTheArchivedView(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := pressKeys(t, archiveModel(t, fc), "zA")

	assert.Equal(t, []bool{true, true}, fc.archiveView)
	require.Len(t, fc.archives, 1)
	assert.False(t, fc.archives[0].archived, "the key restores what the archived view shows")
	assert.Contains(t, m.View().Content, "archived threads")
}
