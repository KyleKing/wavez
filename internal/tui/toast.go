package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

// toastDuration is how long one toast holds the footer before the next
// queued one takes over.
const toastDuration = 4 * time.Second

// toastStates are the transitions DESIGN.md's footer toast fires for: a
// thread parking on a question, finishing, or failing. Both verify_failed
// and a tripped bound land in StateFailed, told apart only by Step's text,
// since the protocol carries no separate state for either.
var toastStates = map[event.State]bool{
	event.StateNeedsIn: true,
	event.StateDone:    true,
	event.StateFailed:  true,
}

// toastState is the footer toast's queue: at most one message shown at a
// time, dismissed by its timer or any keypress, and seen tracks each
// thread's last-known state so a first sighting never toasts.
type toastState struct {
	seen    map[string]event.State
	current string
	queue   []string
	gen     int
}

func newToastState() toastState {
	return toastState{seen: map[string]event.State{}}
}

// toastTickMsg clears the toast that was current when it was scheduled. Its
// gen guards a stale tick from clearing a toast that has already advanced.
type toastTickMsg struct{ gen int }

func toastTick(gen int) tea.Cmd {
	return tea.Tick(toastDuration, func(time.Time) tea.Msg { return toastTickMsg{gen: gen} })
}

// threadLabel names a thread the way Home itself does: root-qualified in the
// fleet scope, bare name in the scoped launch root, since a Root-qualified
// label outside the fleet would name the thread by a directory the screen
// never shows.
func threadLabel(t api.ThreadInfo, fleet bool) string {
	if !fleet {
		return t.Name
	}

	return rootBase(t.Root) + "/" + t.Name
}

// toastDetail is the "what happened" half of a toast's text. A failed
// thread's Step already carries the daemon's reason (a gate failure, a
// verify_failed report, a tripped bound), so it stands in for a fixed
// phrase where the fixed phrase for the other two states is enough.
func toastDetail(st event.State, step string) string {
	switch st {
	case event.StateNeedsIn:
		return "needs input"
	case event.StateDone:
		return "done"
	case event.StateFailed:
		if step = strings.TrimSpace(step); step != "" {
			return step
		}

		return "failed"
	default:
		return string(st)
	}
}

// noteThreadState records id's latest state and queues a toast when this is
// a transition into a toast-worthy state, skipping a first sighting and the
// thread currently open in the thread view while it needs input, since that
// screen's own prompt row is already live.
func (m *Model) noteThreadState(id, label string, st event.State, step string) {
	prev, known := m.toast.seen[id]
	m.toast.seen[id] = st

	if !known || prev == st || !toastStates[st] {
		return
	}
	if id == m.thread.activeID && m.top() == screenThread && st == event.StateNeedsIn {
		return
	}

	m.toast.queue = append(m.toast.queue, glyph(st, m.ascii)+" "+label+" "+toastDetail(st, step))
}

// advanceToast starts the next queued toast when none is showing, and
// reports the tea.Cmd that clears it after toastDuration.
func (m Model) advanceToast() (Model, tea.Cmd) {
	if m.toast.current != "" || len(m.toast.queue) == 0 {
		return m, nil
	}

	m.toast.current, m.toast.queue = m.toast.queue[0], m.toast.queue[1:]
	m.toast.gen++

	return m, toastTick(m.toast.gen)
}

// dismissToast clears whatever toast is showing, for any keypress to call
// before the key is otherwise handled. It does not itself advance the
// queue's next entry; the caller does that once the key has been dispatched,
// so a key that changes screens is judged against the screen it lands on.
func (m *Model) dismissToast() {
	m.toast.current = ""
}

// applyToast overlays the toast's text on out's bottom border line in place
// of whatever footer the screen rendered, since every screen's frame ends in
// exactly one footer line and the toast pre-empts it while shown.
func (m Model) applyToast(out string) string {
	i := strings.LastIndex(out, "\n")
	if i < 0 {
		return out
	}

	return out[:i+1] + m.th.borderFocus.Render(rule('└', '┘', m.toast.current, m.width))
}
