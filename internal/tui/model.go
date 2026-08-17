// Package tui is the wavez terminal client: a flat Bubble Tea v2 model over
// api.Client, rendering Home, Thread, Inbox, Diagnostics, and the palette in
// the lazygit-shaped persistent multi-panel layout DESIGN.md specifies.
package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

// screenKind names one full-frame view in the navigation stack.
type screenKind int

// Screens the stack may hold. The stack's root is always screenHome.
const (
	screenHome screenKind = iota
	screenThread
	screenInbox
	screenDiagnostics
	screenNewThread
)

const (
	minWidth  = 80
	minHeight = 24
)

// Options configures a new Model.
type Options struct {
	Now     func() time.Time
	Dir     string
	NoColor bool
}

// Model is wavez's single top-level Bubble Tea model. Every screen is a
// field on it and every render is a method in that screen's file; there are
// no nested tea.Model sub-programs.
type Model struct {
	th          theme
	client      daemonClient
	transcripts map[string]*transcript
	diffs       map[string][]diffRow
	now         func() time.Time
	status      string
	dir         string
	threads     []api.ThreadInfo
	stack       []screenKind
	pending     []api.PendingInfo
	thread      threadState
	form        threadForm
	palette     paletteState
	restore     restoreState
	inbox       inboxState
	home        homeState
	diag        api.Diagnostics
	width       int
	focus       int
	height      int
	quitting    bool
	ready       bool
	ascii       bool
	noColor     bool
	help        bool
}

// New builds a Model ready to run, before the first WindowSizeMsg arrives.
func New(opts Options) Model {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	th := newTheme(opts.NoColor)

	return Model{
		stack:       []screenKind{screenHome},
		th:          th,
		noColor:     opts.NoColor,
		ascii:       opts.NoColor,
		dir:         opts.Dir,
		now:         now,
		transcripts: map[string]*transcript{},
		diffs:       map[string][]diffRow{},
		home:        newHomeState(th),
		thread:      newThreadState(th),
		form:        newThreadForm(th),
		inbox:       newInboxState(th),
		palette:     newPaletteState(th),
	}
}

// Init satisfies tea.Model. Connecting to the daemon happens outside the
// model, in the bridge Run wires up, so there is nothing to kick off here.
func (Model) Init() tea.Cmd { return nil }

func (m Model) top() screenKind { return m.stack[len(m.stack)-1] }

func (m *Model) push(s screenKind) {
	if m.top() == s {
		return
	}

	m.stack = append(m.stack, s)
	m.focus = 0
}

// popOrClose implements Esc: close an overlay first, then go up one level
// in the screen stack, and do nothing at the root. Esc never quits.
func (m *Model) popOrClose() {
	if m.help {
		m.help = false

		return
	}
	if m.palette.open {
		m.palette.open = false

		return
	}
	if m.restore.open {
		m.restore = restoreState{}
		m.status = "undo canceled"

		return
	}
	if m.top() == screenThread && m.thread.search.visible() {
		m.clearSearch()

		return
	}
	if m.top() == screenHome && m.home.filtering {
		m.home.filtering = false
		m.home.filterInput.Blur()
		m.home.filterInput.Reset()

		return
	}
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
		m.focus = 0
	}
}

func (m Model) panelCount() int {
	if m.top() == screenThread {
		return focusInput + 1
	}

	return 1
}

// Update satisfies tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.thread.input.SetWidth(fitInput(msg.Width, true))
		m.home.filterInput.SetWidth(fitInput(msg.Width, false))
		m.thread.search.input.SetWidth(fitInput(msg.Width, false))
		m.palette.input.SetWidth(fitInput(msg.Width, false))
		m.form.prompt.SetWidth(fitInput(msg.Width, true))

		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case api.Reply:
		m.applyReply(msg)

		return m, nil

	case batchMsg:
		for i := range msg {
			m.applyReply(msg[i])
		}

		return m, nil

	case clientReadyMsg:
		m.client = msg.c

		return m, nil

	case restoreErrMsg:
		m.restore = restoreState{}
		m.status = "undo failed: " + msg.err.Error()

		return m, nil

	case connErrMsg:
		if msg.err != nil {
			m.status = "connection error: " + msg.err.Error()
		} else {
			m.status = "daemon closed the connection"
		}

		return m, nil
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	s := msg.String()

	if mm, cmd, handled := m.handleGlobalKey(s); handled {
		return mm, cmd
	}

	if m.help {
		return m, nil
	}
	if m.palette.open {
		return m.updatePaletteKey(msg, s)
	}
	if m.restore.open {
		return m.updateRestoreKey(s)
	}

	return m.dispatchScreenKey(msg, s)
}

// capturingText reports whether a text field currently owns keystrokes, so
// handleGlobalKey does not steal a letter the user meant to type (an "i" in
// the palette search, a "D" in the filter box) as a screen shortcut.
// Focus follows the panel: the thread's message input takes keyboard focus whenever
// the input panel is the focused one. Bubbles' textinput drops every key
// while blurred, so moving the focus index alone left the input dead and
// its letters falling through to the screen's verb keys.
func (m Model) syncInputFocus() Model {
	if m.top() != screenThread {
		return m
	}

	if m.focus == focusInput {
		m.thread.input.Focus()

		return m
	}

	m.thread.input.Blur()

	return m
}

func (m Model) capturingText() bool {
	switch {
	case m.palette.open, m.restore.open:
		return true
	case m.top() == screenHome && (m.home.filtering || m.home.answerActive):
		return true
	case m.top() == screenInbox && m.inbox.answering:
		return true
	case m.top() == screenThread && (m.thread.search.editing || m.focus == focusInput):
		return true
	case m.top() == screenNewThread:
		return true
	default:
		return false
	}
}

func (m Model) handleGlobalKey(s string) (Model, tea.Cmd, bool) {
	switch s {
	case "ctrl+c":
		m.quitting = true

		return m, tea.Quit, true
	case keyEsc:
		m.popOrClose()

		return m, nil, true
	case keyTab:
		m.focus = (m.focus + 1) % m.panelCount()

		return m.syncInputFocus(), nil, true
	case "shift+tab":
		m.focus = (m.focus - 1 + m.panelCount()) % m.panelCount()

		return m.syncInputFocus(), nil, true
	}

	if m.capturingText() {
		return m, nil, false
	}

	return m.handleGlobalShortcut(s)
}

func (m Model) handleGlobalShortcut(s string) (Model, tea.Cmd, bool) {
	switch s {
	case "q":
		if m.top() != screenHome {
			return m, nil, false
		}

		m.quitting = true

		return m, tea.Quit, true
	case "?":
		m.help = !m.help

		return m, nil, true
	case ":":
		m.palette.open = true

		return m, m.palette.input.Focus(), true
	case "D":
		m.push(screenDiagnostics)

		return m, nil, true
	case "i":
		m.push(screenInbox)

		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) dispatchScreenKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	switch m.top() {
	case screenHome:
		return m.updateHomeKey(msg, s)
	case screenThread:
		return m.updateThreadKey(msg, s)
	case screenInbox:
		return m.updateInboxKey(msg, s)
	case screenDiagnostics:
		return m, nil
	case screenNewThread:
		return m.updateNewThreadKey(msg, s)
	default:
		return m, nil
	}
}

// batchMsg is a coalesced burst of daemon replies, delivered at most once
// per flush window so a stream of token events costs one redraw.
type batchMsg []api.Reply

// connErrMsg reports the client connection ending, with a nil err for a
// clean daemon-initiated close.
type connErrMsg struct{ err error }

func (m *Model) applyReply(r api.Reply) {
	switch r.Kind {
	case api.RepThreads:
		m.replaceThreads(r.Threads)
	case api.RepThread:
		if r.Thread != nil {
			m.upsertThread(*r.Thread)
		}
	case api.RepPending:
		m.pending = r.Pending
	case api.RepDiag:
		if r.Diag != nil {
			m.diag = *r.Diag
		}
	case api.RepEvent:
		if r.Event != nil {
			m.appendEvent(*r.Event)
		}
	case api.RepDiff:
		if r.Diff != nil {
			m.diffs[r.Diff.ThreadID] = parseDiff(r.Diff.Unified)
		}
	case api.RepRestore:
		if r.Restore != nil {
			m.applyRestore(*r.Restore)
		}
	case api.RepError:
		m.status = r.Error
	case api.RepHello, api.RepLagged:
		// Hello is consumed by Dial; a lagged subscription resubscribes in
		// the bridge, not here.
	}
}

// replaceThreads takes Home's polled list. It is the only path that sees
// most threads at all, so the failure notice has to run here too and not
// only on a reply about one thread.
func (m *Model) replaceThreads(next []api.ThreadInfo) {
	for i := range next {
		if prev, ok := m.threadByID(next[i].ID); ok {
			m.notePinnedFailure(prev, next[i])
		}
	}

	m.threads = next
}

func (m *Model) threadByID(id string) (api.ThreadInfo, bool) {
	for i := range m.threads {
		if m.threads[i].ID == id {
			return m.threads[i], true
		}
	}

	return api.ThreadInfo{}, false
}

func (m *Model) upsertThread(info api.ThreadInfo) {
	for i := range m.threads {
		if m.threads[i].ID == info.ID {
			m.notePinnedFailure(m.threads[i], info)
			m.threads[i] = info

			return
		}
	}

	m.threads = append(m.threads, info)
}

// notePinnedFailure says how to get off a broken tier the moment one fails.
// A pin wins over the router's own escalation, so a run pinned to a tier
// that cannot stream stops instead of trying the other one, and nothing else
// in the frame explains why.
func (m *Model) notePinnedFailure(prev, next api.ThreadInfo) {
	if next.Override == "" || next.State != event.StateFailed {
		return
	}
	// Seq, not the state alone: a thread that failed, ran again, and failed
	// again may never be polled while it is working, so the state never
	// leaves failed between two distinct failures.
	if prev.State == event.StateFailed && prev.Seq == next.Seq {
		return
	}

	m.status = next.Name + " failed while pinned to " + routeLabel(next.Override) + "; press m to reroute"
}

func (m *Model) appendEvent(e event.Event) {
	tr := m.transcripts[e.ThreadID]
	if tr == nil {
		tr = &transcript{}
		m.transcripts[e.ThreadID] = tr
	}

	tr.append(e)
}
