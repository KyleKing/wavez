package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/link"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
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

func TestTranscript_DropsEmptyStateAndAgentRows(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindState, State: event.StateWorking})
	tr.append(event.Event{Kind: event.KindAgent, Text: ""})
	tr.append(event.Event{Kind: event.KindState, State: event.StateGating, Text: "queued for review"})
	tr.append(event.Event{Kind: event.KindTool, Text: "ran gofmt"})

	require.Len(t, tr.rows, 2)
	assert.Equal(t, event.KindState, tr.rows[0].kind)
	assert.Equal(t, "queued for review", tr.rows[0].text)
	assert.Equal(t, event.KindTool, tr.rows[1].kind)
}

func TestTranscript_RoleMarkerTypesPrecedingRowWithoutAddingOne(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: "done, no more calls needed."})
	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleAnswer})

	require.Len(t, tr.rows, 1)
	assert.Equal(t, event.RoleAnswer, tr.rows[0].role)
	assert.True(t, tr.rows[0].expanded, "an answer row expands by default")
}

func TestTranscript_ToggleKeepsUserChoiceAcrossRoleArrival(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: "about to call a tool."})
	require.True(t, tr.toggle(0))
	assert.True(t, tr.rows[0].expanded)

	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleNote})

	assert.Equal(t, event.RoleNote, tr.rows[0].role)
	assert.True(t, tr.rows[0].expanded, "a user toggle survives a later role marker")
}

func TestTranscript_ToggleReportsWhetherIndexNamedARow(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindTool, Text: "ran gofmt"})

	assert.True(t, tr.toggle(0))
	assert.False(t, tr.toggle(1))
	assert.False(t, tr.toggle(-1))
	assert.Equal(t, 1, tr.count())
}

func TestTranscript_RenderExpandsAnswerAndFoldsNote(t *testing.T) {
	t.Parallel()

	th := newTheme(true)

	longAnswer := "This reply runs on well past a single row so it needs to wrap " +
		"across several lines to be read in full."
	longNote := "Quick note before the next tool call, also long enough to wrap " +
		"if it were expanded."

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: longAnswer})
	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleAnswer})
	tr.append(event.Event{Kind: event.KindAgent, Text: longNote})
	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleNote})

	require.Len(t, tr.rows, 2)

	const width = 40

	answerLines := renderRowLines(tr.rows[0], width, th, "", false, link.Table{})
	noteLines := renderRowLines(tr.rows[1], width, th, "", false, link.Table{})

	assert.Greater(t, len(answerLines), 1, "an expanded answer wraps across multiple lines")
	assert.Len(t, noteLines, 1, "a folded note stays one line")
}

// lipgloss measures a tab as one cell where the terminal renders eight, so a
// tab reaching a rendered row walks the frame's right border off that row.
func TestTranscript_ExpandedRowRendersNoTab(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{
		Kind: event.KindTool, Tool: "read",
		Text: "internal/tui/thread.go (lines 1-3 of 3):\nfunc x() {\n\tif y {\n\t\treturn\n\t}\n}",
	})
	require.Len(t, tr.rows, 1)

	r := tr.rows[0]
	r.expanded = true

	for _, line := range renderRowLines(r, 110, newTheme(true), "", false, link.Table{}) {
		assert.NotContains(t, line, "\t", "a rendered row carries no tab")
	}
}

func TestTranscript_FoldedToolRowShowsItsHeadline(t *testing.T) {
	t.Parallel()

	th := newTheme(true)

	// Wide enough that every case's first line fits whole, so what the
	// assertions see is the fold rule, not the truncation rule.
	const width = 110

	cases := []struct {
		name     string
		tool     string
		text     string
		foldHas  string // what the one folded line must carry
		foldLack string // what it must not carry: body left to the expanded row
	}{
		{
			name: "read carries its file header and a body below",
			tool: "read",
			text: "internal/tui/thread.go (lines 295-345 of 982):\n295 }\n" +
				"296 case \"y\", \"n\", \"a\":\n297 // A pendi",
			foldHas:  "read internal/tui/thread.go (lines 295-345 of 982):…",
			foldLack: "295 }",
		},
		{
			name: "search folds to its summary line",
			tool: "search",
			text: "20 results, of 99 that matched; raise limit or narrow the query to see the rest\n" +
				"internal/tui/thread.go\ninternal/tui/home.go",
			foldHas:  "search 20 results, of 99 that matched; raise limit or narrow the query to see the rest…",
			foldLack: "internal/tui/thread.go",
		},
		{
			name:    "shell folds to its exit code",
			tool:    "shell",
			text:    "exit code: 0\nGOOS=\"darwin\"\nGOARCH=\"arm64\"",
			foldHas: "shell exit code: 0…",
		},
		{
			name:    "str_replace has nothing under its headline",
			tool:    "str_replace",
			text:    "internal/tui/thread.go: +46 -26 lines (now lines 581-623)",
			foldHas: "str_replace internal/tui/thread.go: +46 -26 lines (now lines 581-623)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := &transcript{}
			tr.append(event.Event{Kind: event.KindTool, Tool: tc.tool, Text: tc.text})
			require.Len(t, tr.rows, 1)
			require.False(t, tr.rows[0].expanded, "a tool row folds by default")

			r := tr.rows[0]
			folded := renderRowLines(r, width, th, "", false, link.Table{})
			require.Len(t, folded, 1, "a folded row is one line")

			foldLine := ansi.Strip(folded[0])
			assert.Contains(t, foldLine, tc.foldHas)
			if tc.foldLack != "" {
				assert.NotContains(t, foldLine, tc.foldLack,
					"a folded row spends its width on the headline, not the body")
			}

			r.expanded = true

			expanded := renderRowLines(r, width, th, "", false, link.Table{})
			body := strings.Join(expanded, "\n")
			for _, ln := range strings.Split(tc.text, "\n")[1:] {
				if ln == "" {
					continue
				}

				assert.Contains(t, body, ln, "an expanded row still carries the whole result")
			}
		})
	}
}

func TestTranscript_LinkedIdentifierDoesNotWidenTheWrap(t *testing.T) {
	t.Parallel()

	th := newTheme(true)

	tbl, err := link.Compile([]link.Source{
		{Pattern: `#(\d+)`, URL: "https://github.com/kyleking/wavez/pull/$1"},
	})
	require.NoError(t, err)

	text := "please take a look at #123 when you have a minute to review it fully"

	for _, width := range []int{20, 30, 40, 60} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			t.Parallel()

			trUnlinked := &transcript{}
			trUnlinked.append(event.Event{Kind: event.KindAgent, Text: text})
			trUnlinked.append(event.Event{Kind: event.KindAgent, Role: event.RoleAnswer})

			trLinked := &transcript{}
			trLinked.append(event.Event{Kind: event.KindAgent, Text: text})
			trLinked.append(event.Event{Kind: event.KindAgent, Role: event.RoleAnswer})

			unlinkedLines := renderRowLines(trUnlinked.rows[0], width, th, "", false, link.Table{})
			linkedLines := renderRowLines(trLinked.rows[0], width, th, "", false, tbl)

			require.Len(t, linkedLines, len(unlinkedLines),
				"a hyperlink escape sequence must not count toward the wrap width")

			for i, line := range linkedLines {
				assert.LessOrEqual(t, lipgloss.Width(line), width,
					"linked line %d exceeds the render width", i)
			}

			joined := strings.Join(linkedLines, "")
			assert.Contains(t, joined, "\x1b]8;;https://github.com/kyleking/wavez/pull/123\x1b\\",
				"the identifier should render as an OSC 8 hyperlink")
		})
	}
}

func TestTranscript_LineCountChangesWithToggle(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: strings.Repeat("word ", 40)})
	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleNote})

	const width = 30

	folded := tr.lineCount(width, catNone)
	require.True(t, tr.toggle(0))
	expanded := tr.lineCount(width, catNone)

	assert.Equal(t, 1, folded)
	assert.Greater(t, expanded, folded)
}

func TestTranscript_RowAtLineRoundTrips(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: strings.Repeat("word ", 40)})
	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleAnswer})
	tr.append(event.Event{Kind: event.KindTool, Text: "ran gofmt", Seq: 1})

	const width = 24

	total := tr.lineCount(width, catNone)
	require.Greater(t, total, 1)

	for line := range total {
		row := tr.rowAtLine(width, catNone, line)
		require.GreaterOrEqual(t, row, 0)
		require.Less(t, row, tr.count())
	}

	assert.Equal(t, 1, tr.rowAtLine(width, catNone, total-1), "the last line belongs to the trailing tool row")
	assert.Equal(t, -1, tr.rowAtLine(width, catNone, total))
	assert.Equal(t, -1, tr.rowAtLine(width, catNone, -1))
}

func TestTranscript_RenderWindowsOffsetInLinesAtTopAndBottom(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindAgent, Text: strings.Repeat("word ", 40)})
	tr.append(event.Event{Kind: event.KindAgent, Role: event.RoleAnswer})

	const width = 30

	total := tr.lineCount(width, catNone)
	require.Greater(t, total, 3)

	th := newTheme(true)

	bottom := tr.render(renderOpts{width: width, height: 2, offset: 0, cursor: -1, theme: th})
	require.Len(t, bottom, 2)

	all := tr.render(renderOpts{width: width, height: total, offset: 0, cursor: -1, theme: th})
	require.Len(t, all, total)
	assert.Equal(t, all[len(all)-2:], bottom, "a zero offset windows from the very bottom")

	const window = 2

	top := tr.render(renderOpts{width: width, height: window, offset: total - window, cursor: -1, theme: th})
	assert.Equal(t, all[:window], top, "the offset that reaches the top shows the first lines")

	overshoot := tr.render(renderOpts{width: width, height: window, offset: total + 10, cursor: -1, theme: th})
	assert.Equal(t, top, overshoot, "an offset past the top clamps to the top instead of going empty")

	assert.Equal(t, 0, tr.rowAtLine(width+10, catNone, 0), "rowAtLine tracks a rewrap at a different width")
}

func TestRow_Category(t *testing.T) {
	t.Parallel()

	edited := row{kind: event.KindTool, tool: "write", changes: []tool.Change{{Path: "a.go"}}}

	answer := row{kind: event.KindAgent, role: event.RoleAnswer}

	tests := []struct {
		name string
		want filterCategory
		row  row
	}{
		{"a tool row with file changes is an edit", catEdit, edited},
		{"a shell tool row with no changes is a shell", catShell, row{kind: event.KindTool, tool: "shell"}},
		{"a read-only tool row is neither", catNone, row{kind: event.KindTool, tool: "read"}},
		{"a gate row is a gate", catGate, row{kind: event.KindGate}},
		{"a permission row is a permission", catPermission, row{kind: event.KindPermission}},
		{"an agent row with the answer role is an answer", catAnswer, answer},
		{"an agent row with the note role is neither", catNone, row{kind: event.KindAgent, role: event.RoleNote}},
		{"a user row is neither", catNone, row{kind: event.KindUser}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.row.category())
		})
	}
}

func TestTranscript_VisibleRows_KeepsOnlyOneCategory(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{
		Kind: event.KindTool, Tool: "write", Text: "wrote a.go", Changes: []tool.Change{{Path: "a.go"}},
	})
	tr.append(event.Event{Kind: event.KindTool, Tool: "shell", Text: "ran go test"})
	tr.append(event.Event{Kind: event.KindGate, Text: "format passed"})

	assert.Equal(t, []int{0}, tr.visibleRows(catEdit))
	assert.Equal(t, []int{1}, tr.visibleRows(catShell))
	assert.Equal(t, []int{2}, tr.visibleRows(catGate))
	assert.Empty(t, tr.visibleRows(catAnswer), "a category with no matching rows keeps none")
	assert.Equal(t, []int{0, 1, 2}, tr.visibleRows(catNone), "catNone keeps every row")
}

func TestNextFilterCategory_CyclesAndWrapsBackToAll(t *testing.T) {
	t.Parallel()

	cat := catNone

	seen := make([]filterCategory, 0, len(filterCategories))
	for range filterCategories {
		cat = nextFilterCategory(cat)
		seen = append(seen, cat)
	}

	assert.Equal(t, append(filterCategories[1:], catNone), seen,
		"cycling once through every category returns to catNone (all)")
}

func TestTranscript_FuzzySearchFindsWordsSubstringWouldMiss(t *testing.T) {
	t.Parallel()

	tr := &transcript{}
	tr.append(event.Event{Kind: event.KindTool, Text: "renamed the lease default"})

	assert.Empty(t, tr.search("default lease", catNone),
		"the words appear out of order, so a literal substring search misses")
	assert.Equal(t, []int{0}, tr.fuzzySearch("default lease", catNone),
		"fuzzy search matches every word regardless of order")
}

func TestFooterHints_DropsLowestPriorityAsWidthShrinks(t *testing.T) {
	t.Parallel()

	hints := []hint{{"enter", "open", ""}, {"v", "peek", ""}, {"i", "inbox", ""}, {"?", "help", ""}}

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
	sent        []sentMsg
	restores    []restoreCall
	routes      []routeCall
	archives    []archiveCall
	archiveView []bool
	thinks      []thinkCall
	ranRoutines []string
	canceled    []string
	models      []api.Command
	subscribed  []string
	scopes      []bool
	listed      int
	resets      int
}

type archiveCall struct {
	threadID string
	archived bool
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
	threadID   string
	checkpoint string
	confirm    bool
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

type sentMsg struct {
	threadID  string
	text      string
	interrupt bool
}

func (f *fakeClient) routines() tea.Cmd {
	f.listed++

	return nil
}

func (f *fakeClient) runRoutine(name string) tea.Cmd {
	f.ranRoutines = append(f.ranRoutines, name)

	return nil
}

func (f *fakeClient) subscribe(id string) tea.Cmd {
	f.subscribed = append(f.subscribed, id)

	return nil
}

func (f *fakeClient) sendPrompt(threadID, text string, interrupt bool) tea.Cmd {
	f.sent = append(f.sent, sentMsg{threadID: threadID, text: text, interrupt: interrupt})

	return nil
}

func (f *fakeClient) restoreTo(threadID, checkpoint string, confirm bool) tea.Cmd {
	f.restores = append(f.restores, restoreCall{threadID: threadID, checkpoint: checkpoint, confirm: confirm})

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

func (f *fakeClient) archive(threadID string, archived bool) tea.Cmd {
	f.archives = append(f.archives, archiveCall{threadID: threadID, archived: archived})

	return nil
}

func (f *fakeClient) setArchiveView(archived bool) tea.Cmd {
	f.archiveView = append(f.archiveView, archived)

	return nil
}

func (*fakeClient) schedule() tea.Cmd { return nil }

func (f *fakeClient) setScope(fleet bool) tea.Cmd {
	f.scopes = append(f.scopes, fleet)

	return nil
}

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

func (f *fakeClient) resetDiag() tea.Cmd {
	f.resets++

	return nil
}

func (f *fakeClient) modelCommand(cmd api.Command) tea.Cmd {
	f.models = append(f.models, cmd)

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

// Enter has no answer role of its own: from the transcript it only ever
// toggles the row under the cursor, so a pending permission still needs
// y/n/a and is never resolved by mistake.
func TestThreadKeys_EnterDoesNotAnswerAPendingPermission(t *testing.T) {
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

	m = pressKey(t, m, 0, "enter")
	assert.Empty(t, fc.answered, "enter must not answer a pending permission")

	m = pressKey(t, m, 'y')
	require.Len(t, fc.answered, 1)
	assert.Equal(t, permission.Allow, fc.answered[0].decision)
}

// Enter only sends from the composer; the transcript's own Enter toggles a
// row instead, so this is the one path that must keep sending.
func TestThreadCompose_EnterSendsFromComposer(t *testing.T) {
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
	m.focus = focusInput
	m.thread.input.Focus()

	for _, r := range "hi" {
		m = pressKey(t, m, r)
	}

	m = pressKey(t, m, 0, "enter")

	require.Len(t, fc.sent, 1)
	assert.Equal(t, "t1", fc.sent[0].threadID)
	assert.Equal(t, "hi", fc.sent[0].text)
	assert.Empty(t, m.thread.input.Value(), "the composer clears after sending")
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
		case "up":
			msg.Code = tea.KeyUp
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

// keyMsg builds one key press from the string form the screens switch on.
func keyMsg(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func modelScreenModel(fc *fakeClient) Model {
	m := New(Options{NoColor: true})
	m.client = fc
	m.width, m.height, m.ready = 120, 40, true
	m.push(screenModels)
	m.models.list = []api.ModelInfo{{Name: "qwen3:8b", SizeBytes: 1}}

	return m
}

// TestModelScreen_UninstallPreviewsBeforeActing covers the whole path from
// the client's side: the first key asks the daemon what the action costs, and
// only a confirmation carries Confirm.
func TestModelScreen_UninstallPreviewsBeforeActing(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := modelScreenModel(fc)

	m, _ = m.updateModelsKey(keyMsg("x"), "x")

	if len(fc.models) != 1 || fc.models[0].Kind != api.CmdModelRemove || fc.models[0].Confirm {
		t.Fatalf("first key issued %+v, want an unconfirmed remove", fc.models)
	}

	m, _ = m.updateModelsKey(keyMsg("y"), "y")

	if len(fc.models) != 1 {
		t.Fatalf("yes before the disk delta arrived issued %+v, want nothing", fc.models)
	}

	m.applyModels(api.Reply{Models: m.models.list, Note: "removing qwen3:8b frees 4.9 GB"})

	m, _ = m.updateModelsKey(keyMsg("y"), "y")

	if len(fc.models) != 2 || !fc.models[1].Confirm {
		t.Fatalf("confirming issued %+v, want a confirmed remove", fc.models)
	}
	if m.models.action != "" {
		t.Errorf("action = %q, want the confirmation closed", m.models.action)
	}
}

// TestModelScreen_ListFitsTheTerminal covers the case this machine cannot
// show: more models than rows, where an unbounded list pushed the key hints
// off the screen.
func TestModelScreen_ListFitsTheTerminal(t *testing.T) {
	t.Parallel()

	m := modelScreenModel(&fakeClient{})
	m.height = 12
	m.models.list = nil

	for i := range 20 {
		m.models.list = append(m.models.list, api.ModelInfo{Name: fmt.Sprintf("model-%02d", i), SizeBytes: 1})
	}

	m.models.cursor = 15

	lines := strings.Split(m.renderModels(), "\n")
	if len(lines) > m.height {
		t.Fatalf("rendered %d lines into a %d-row terminal", len(lines), m.height)
	}

	frame := strings.Join(lines, "\n")
	for _, want := range []string{"> model-15", "showing", "[esc]back"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "model-00") {
		t.Errorf("a row far from the cursor was drawn:\n%s", frame)
	}
}

// TestModelScreen_DeclinedConfirmationActsOnNothing is the case that matters
// most: nothing on disk changes without a yes.
func TestModelScreen_DeclinedConfirmationActsOnNothing(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := modelScreenModel(fc)

	m, _ = m.updateModelsKey(keyMsg("x"), "x")
	m, _ = m.updateModelsKey(keyMsg("n"), "n")

	for _, call := range fc.models {
		if call.Confirm {
			t.Fatalf("declining still issued %+v", fc.models)
		}
	}
	if m.models.action != "" {
		t.Errorf("action = %q, want the confirmation closed", m.models.action)
	}
}

func TestDiagnostics_ResetAsksTheDaemonToClearTheWindow(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := New(Options{NoColor: true})
	m.client = fc
	m.push(screenDiagnostics)

	if _, _ = m.updateDiagnosticsKey("r"); fc.resets != 1 {
		t.Fatalf("resets = %d, want the window cleared once", fc.resets)
	}
}

// TestHome_PeekSubscribesToAnUnvisitedThread covers the fleet case: a thread
// nobody has opened has no events on the client, so the peek must ask for
// them or open onto nothing.
func TestHome_PeekSubscribesToAnUnvisitedThread(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := New(Options{NoColor: true})
	m.client = fc
	m.threads = []api.ThreadInfo{{ID: "t1", Name: "one", Dir: "d"}}

	m, _ = m.updateHomeKey(keyMsg("v"), "v")
	m, _ = m.updateHomeKey(keyMsg("v"), "v")
	m, _ = m.updateHomeKey(keyMsg("v"), "v")

	if len(fc.subscribed) != 2 || fc.subscribed[0] != "t1" {
		t.Fatalf("subscribed = %v, want t1 on each expand and never on collapse", fc.subscribed)
	}
	if !m.home.expanded["t1"] {
		t.Error("third press should leave the row expanded")
	}
}

func TestHomeKey_ScopeTogglesFleetAndRequestsAnUnscopedList(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := homeFixture(t, fc, nil, nil)

	m = pressKey(t, m, 'w')
	assert.True(t, m.home.fleet)
	require.Len(t, fc.scopes, 1)
	assert.True(t, fc.scopes[0], "toggling to fleet scope should ask the client for every root")

	m = pressKey(t, m, 'w')
	assert.False(t, m.home.fleet)
	require.Len(t, fc.scopes, 2)
	assert.False(t, fc.scopes[1], "toggling back should ask the client to scope again")
}

// TestHome_OpenThreadWorksForAFleetRowFromAnotherRoot covers the fleet
// lane's routing claim for Home: a row from a root other than the launch
// root subscribes and opens the same as any other, since thread ids are
// globally unique and no command here carries a root to filter by.
func TestHome_OpenThreadWorksForAFleetRowFromAnotherRoot(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	threads := []api.ThreadInfo{{ID: "other-root-thread", Name: "flaky-ci", Root: "/repo/yak-shears"}}
	m := homeFixture(t, fc, threads, nil)

	m, _ = m.updateHomeKey(keyMsg("enter"), keyEnter)

	require.Len(t, fc.subscribed, 1)
	assert.Equal(t, "other-root-thread", fc.subscribed[0])
	assert.Equal(t, screenThread, m.top())
}

func TestModelScreen_EscClosesOverlaysBeforeLeavingTheScreen(t *testing.T) {
	t.Parallel()

	m := modelScreenModel(&fakeClient{})
	m, _ = m.updateModelsKey(keyMsg("e"), "e")
	m, _ = m.updateModelsKey(keyMsg("e"), "e")

	m.popOrClose()
	if !m.models.settings || m.models.editing {
		t.Fatalf("first esc should close the edit field only, got settings=%v editing=%v",
			m.models.settings, m.models.editing)
	}

	m.popOrClose()
	if m.models.settings || m.top() != screenModels {
		t.Fatalf("second esc should close the settings pane and stay on the screen, got %v", m.top())
	}

	m.popOrClose()
	if m.top() == screenModels {
		t.Fatal("third esc should leave the screen")
	}
}

func TestThreadHints_NameEnterOnlyWhereItBinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		want  string
		focus int
		mode  vimMode
	}{
		{"composer sends", "[enter]send", focusInput, modeInsert},
		{"transcript toggles", "[enter]toggle", focusTranscript, modeNormal},
		// Esc leaves insert and does nothing in normal, so naming it there
		// pointed at a key that would not move.
		{"insert names the way out", "[esc]normal mode", focusInput, modeInsert},
		{"normal names the way in", "[i]insert", focusInput, modeNormal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := footerHints(threadHints(searchState{}, tc.focus, catNone, tc.mode), 200)
			assert.Contains(t, got, tc.want)
		})
	}

	t.Run("normal mode never names esc", func(t *testing.T) {
		t.Parallel()

		got := footerHints(threadHints(searchState{}, focusInput, catNone, modeNormal), 200)
		assert.NotContains(t, got, "[esc]")
	})

	t.Run("diff pane names no enter", func(t *testing.T) {
		t.Parallel()

		got := footerHints(threadHints(searchState{}, focusDiff, catNone, modeNormal), 200)
		assert.NotContains(t, got, "[enter]")
	})
}

// The goal is in the header when the width allows it and behind `g` when it
// does not, so coming back to a thread never means reading the transcript to
// remember what it was for.
func TestGoal_HeaderWhenItFitsKeyWhenItDoesNot(t *testing.T) {
	t.Parallel()

	const goal = "make the lease TTL configurable from pkl"

	m := New(Options{NoColor: true})
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	rm, ok := resized.(Model)
	require.True(t, ok)
	m = rm

	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "fix-lock", Dir: "wavez", Goal: goal, State: event.StateWorking},
	}})
	m.thread.activeID = "t1"
	m.stack = append(m.stack, screenThread)

	assert.Contains(t, m.render(), "make the lease TTL", "a wide header should carry the goal")

	long := "wavez · fix-lock-timeout · qwen3:8b 2.8k/8.2k · $0.00 · filtered · 2 need input"
	assert.Equal(t, long, headerGoal(long, goal, minWidth),
		"a header with no room left should drop the goal rather than truncate it to nothing")

	opened := pressKey(t, m, 'g')
	assert.True(t, opened.goal, "g should open the goal")
	assert.Contains(t, opened.render(), "configurable", "the goal panel should hold the whole goal")
}
