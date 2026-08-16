package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

func TestTranscript_CoalescesAgentText(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: "The lease "})
	tr.append(event.Event{Kind: event.KindAgent, Text: "TTL is "})
	tr.append(event.Event{Kind: event.KindAgent, Text: "now configurable."})
	tr.append(event.Event{Kind: event.KindTool, Text: "ran gofmt"})

	require.Len(t, tr.rows, 2)
	assert.Equal(t, "The lease TTL is now configurable.", tr.rows[0].text)
	assert.Equal(t, event.KindTool, tr.rows[1].kind)
}

func TestTranscript_VisibleWindow(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	for i := range 500 {
		tr.append(event.Event{Kind: event.KindTool, Text: "step", Seq: uint64(i)})
	}

	const height = 10

	tail := tr.visible(height, 0)
	require.Len(t, tail, height)
	assert.Equal(t, uint64(499), tail[len(tail)-1].seq)
	assert.Equal(t, uint64(490), tail[0].seq)

	scrolled := tr.visible(height, 5)
	assert.Equal(t, uint64(494), scrolled[len(scrolled)-1].seq)
}

func TestFooterHints_DropsLowestPriorityAsWidthShrinks(t *testing.T) {
	t.Parallel()

	hints := []hint{{"enter", "open"}, {"v", "peek"}, {"i", "inbox"}, {"?", "help"}}

	wide := footerHints(hints, 80)
	narrow := footerHints(hints, 12)

	assert.Contains(t, wide, "[?]help")
	assert.NotContains(t, narrow, "[?]help")
	assert.Contains(t, narrow, "[enter]open")
}

type fakeClient struct {
	answered []answered
}

type answered struct {
	promptID string
	text     string
	decision permission.Decision
}

func (*fakeClient) subscribe(string) tea.Cmd    { return nil }
func (*fakeClient) send(string, string) tea.Cmd { return nil }

func (f *fakeClient) answer(promptID, text string, decision permission.Decision) tea.Cmd {
	f.answered = append(f.answered, answered{promptID: promptID, text: text, decision: decision})

	return nil
}

func TestPermissionAnswer_SendsDecisionInline(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	rm, ok := resized.(Model)
	require.True(t, ok)

	m = rm
	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", Dir: "calcipy", State: event.StateNeedsIn},
	}})
	m.applyReply(api.Reply{Kind: api.RepPending, Pending: []api.PendingInfo{
		{ID: "p1", ThreadID: "t1", Thread: "docs-pass", Tool: "shell", Action: "rm -rf .testmondata"},
	}})

	got, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	nm, ok := got.(Model)
	require.True(t, ok)

	require.Len(t, fc.answered, 1)
	assert.Equal(t, "p1", fc.answered[0].promptID)
	assert.Equal(t, permission.Allow, fc.answered[0].decision)
	assert.False(t, nm.home.answerActive)
}
