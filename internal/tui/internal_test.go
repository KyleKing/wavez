package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
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
	answered    []answered
	diffed      []string
	created     []created
	restores    []restoreCall
	routes      []routeCall
	thinks      []thinkCall
	ranRoutines []string
	listed      int
	canceled    []string
}

type routeCall struct {
	threadID string
	override router.Choice
}

type thinkCall struct {
	thinking *bool
	threadID string
}

type restoreCall struct {
	threadID string
	confirm  bool
}

type created struct {
	prompt string
	model  string
	parent string
	cycle  string
	dirs   []string
}

type answered struct {
	promptID string
	text     string
	decision permission.Decision
}

func (f *fakeClient) routines() tea.Cmd {
	f.listed++

	return nil
}

func (f *fakeClient) runRoutine(name string) tea.Cmd {
	f.ranRoutines = append(f.ranRoutines, name)

	return nil
}

func (*fakeClient) subscribe(string) tea.Cmd    { return nil }
func (*fakeClient) send(string, string) tea.Cmd { return nil }

func (f *fakeClient) restore(threadID string, confirm bool) tea.Cmd {
	f.restores = append(f.restores, restoreCall{threadID: threadID, confirm: confirm})

	return nil
}

func (f *fakeClient) think(threadID string, thinking *bool) tea.Cmd {
	f.thinks = append(f.thinks, thinkCall{threadID: threadID, thinking: thinking})

	return nil
}

func (f *fakeClient) route(threadID string, override router.Choice) tea.Cmd {
	f.routes = append(f.routes, routeCall{threadID: threadID, override: override})

	return nil
}

func (*fakeClient) schedule() tea.Cmd { return nil }

func (f *fakeClient) cancel(threadID string) tea.Cmd {
	f.canceled = append(f.canceled, threadID)

	return nil
}

func (f *fakeClient) diff(threadID string) tea.Cmd {
	f.diffed = append(f.diffed, threadID)

	return nil
}

func (f *fakeClient) newThread(prompt, model, parent, cycle string, dirs []string) tea.Cmd {
	f.created = append(f.created, created{
		prompt: prompt, model: model, parent: parent, cycle: cycle, dirs: dirs,
	})

	return nil
}

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

// The three new Thread-view keys share letters with the permission answers,
// which win only while a prompt is focused and the input is empty.
func TestThreadKeys_AskLineForkAndDiff(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	rm, ok := resized.(Model)
	require.True(t, ok)
	m = rm

	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "fix-lock", Dir: "wavez", State: event.StateWorking},
	}})
	m.thread.activeID = "t1"
	m.stack = append(m.stack, screenThread)
	m.applyReply(api.Reply{Kind: api.RepDiff, Diff: &api.Diff{ThreadID: "t1", Unified: sampleDiff}})

	// `d` focuses the diff pane and refetches.
	m = pressKey(t, m, 'd')
	assert.Equal(t, focusDiff, m.focus)
	assert.Equal(t, []string{"t1"}, fc.diffed)

	// Move onto the first added line, then ask about it.
	for range 3 {
		m = pressKey(t, m, 0, "down")
	}

	m = pressKey(t, m, 'a')
	assert.Equal(t, focusInput, m.focus)
	assert.Contains(t, m.thread.input.Value(), "internal/lease/lease.go:")

	// `f` opens the fork form, carrying the parent and creating on enter.
	m.thread.input.SetValue("")
	m.focus = focusDiff
	m = pressKey(t, m, 'f')
	require.Equal(t, screenNewThread, m.top())
	assert.Equal(t, "t1", m.form.parent)

	m = pressKey(t, m, 0, "enter")
	require.Len(t, fc.created, 1)
	assert.Equal(t, "t1", fc.created[0].parent)
}

// A pending prompt owns y/n/a from the transcript panel, where the
// permission row lives. The composer never sees them: `a` there appends.
func TestThreadKeys_PendingPromptStillOwnsAnswers(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	rm, ok := resized.(Model)
	require.True(t, ok)
	m = rm

	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "fix-lock", Dir: "wavez", State: event.StateNeedsIn},
	}})
	m.applyReply(api.Reply{Kind: api.RepPending, Pending: []api.PendingInfo{
		{ID: "p1", ThreadID: "t1", Tool: "shell", Action: "rm -rf .testmondata"},
	}})
	m.thread.activeID = "t1"
	m.stack = append(m.stack, screenThread)
	m.focus = focusTranscript

	m = pressKey(t, m, 'a')
	require.Len(t, fc.answered, 1)
	assert.Equal(t, permission.AllowAlways, fc.answered[0].decision)
	assert.Empty(t, fc.created)
}

// `n` denies a focused prompt, and a live search query takes it back:
// stepping a match costs nothing, denying a prompt the user never read does.
func TestThreadSearch_LiveQueryOwnsTheStepKeys(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"", "lease"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			fc := &fakeClient{}
			m := homeFixture(t, fc,
				[]api.ThreadInfo{{ID: "t1", Name: "fix-lock", Dir: "wavez", State: event.StateNeedsIn}},
				[]api.PendingInfo{{ID: "p1", ThreadID: "t1", Tool: "shell", Action: "rm -rf .testmondata"}})

			m.thread.activeID = "t1"
			m.stack = append(m.stack, screenThread)
			m.focus = focusTranscript
			m.thread.search.query = query
			m.appendEvent(event.Event{ThreadID: "t1", Kind: event.KindTool, Text: "read lease.go"})

			m = pressKey(t, m, 'n')

			if query == "" {
				require.Len(t, fc.answered, 1)
				assert.Equal(t, permission.Deny, fc.answered[0].decision)

				return
			}

			assert.Empty(t, fc.answered, "a live query must not let n deny a prompt")
		})
	}
}

// pressKey sends one key to the model and returns the updated Model. Pass a
// rune for a letter, or zero plus a name for a named key.
func pressKey(t *testing.T, m Model, r rune, name ...string) Model {
	t.Helper()

	msg := tea.KeyPressMsg{Code: r, Text: string(r)}
	if len(name) > 0 {
		msg = tea.KeyPressMsg{Text: name[0]}

		switch name[0] {
		case "down":
			msg.Code = tea.KeyDown
		case "enter":
			msg.Code = tea.KeyEnter
		}
	}

	got, _ := m.Update(msg)

	nm, ok := got.(Model)
	require.True(t, ok)

	return nm
}

// On Home, `n` answers a focused permission prompt and opens the new-thread
// form otherwise, which is the same modal rule Thread view follows.
func TestHomeKey_NewThreadVersusDeny(t *testing.T) {
	t.Parallel()

	threads := []api.ThreadInfo{{ID: "t1", Name: "docs-pass", Dir: "calcipy", State: event.StateNeedsIn}}

	t.Run("with a prompt on the row it denies", func(t *testing.T) {
		t.Parallel()

		fc := &fakeClient{}
		m := homeFixture(t, fc, threads, []api.PendingInfo{{ID: "p1", ThreadID: "t1", Tool: "shell"}})

		m = pressKey(t, m, 'n')
		require.Len(t, fc.answered, 1)
		assert.Equal(t, permission.Deny, fc.answered[0].decision)
		assert.NotEqual(t, screenNewThread, m.top())
	})

	t.Run("with no prompt it opens the form", func(t *testing.T) {
		t.Parallel()

		fc := &fakeClient{}
		m := homeFixture(t, fc, threads, nil)

		m = pressKey(t, m, 'n')
		require.Equal(t, screenNewThread, m.top())
		assert.Empty(t, m.form.parent)
		assert.Empty(t, fc.answered)
	})
}

func homeFixture(t *testing.T, fc *fakeClient, threads []api.ThreadInfo, pending []api.PendingInfo) Model {
	t.Helper()

	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	rm, ok := resized.(Model)
	require.True(t, ok)

	m = rm
	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: threads})
	m.applyReply(api.Reply{Kind: api.RepPending, Pending: pending})

	return m
}

// padRight measures display width but once truncated by rune count, so a
// styled line that exactly filled its frame was cut mid-escape-sequence and
// rendered at a fraction of its width with a stray ellipsis.
func TestPadRight_KeepsStyledLinesWhole(t *testing.T) {
	t.Parallel()

	th := newTheme(false)

	const width = 40

	styled := th.fgEmphasis.Render(strings.Repeat("x", width))
	assert.Equal(t, width, lipgloss.Width(padRight(styled, width)), "a line that exactly fills its frame")

	over := th.fgEmphasis.Render(strings.Repeat("x", width+10))
	assert.Equal(t, width, lipgloss.Width(padRight(over, width)), "a line that overflows its frame")

	under := th.fgEmphasis.Render("short")
	assert.Equal(t, width, lipgloss.Width(padRight(under, width)), "a line shorter than its frame")
}
