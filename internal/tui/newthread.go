package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// threadForm is the form behind `n`: a prompt, and the parent when the form
// was opened as a fork.
type threadForm struct {
	parent string
	prompt textinput.Model
}

func newThreadForm(th theme) threadForm {
	return threadForm{prompt: th.newInput("what should this thread do?")}
}

// openNewThread pushes the form. A non-empty parent makes it a fork, which
// carries the parent's change set and none of its transcript.
func (m Model) openNewThread(parent string) (Model, tea.Cmd) {
	m.form = newThreadForm(m.th)
	m.form.parent = parent
	// Sized here as well as on resize: the form is built after the last
	// WindowSizeMsg, so it would otherwise render one column wide.
	m.form.prompt.SetWidth(fitInput(m.width, true))
	m.form.prompt.Focus()
	m.stack = append(m.stack, screenNewThread)

	return m, nil
}

func (m Model) updateNewThreadKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	// Esc is handled globally, before any screen sees it.
	if s == keyEnter {
		return m.submitNewThread()
	}

	var cmd tea.Cmd
	m.form.prompt, cmd = m.form.prompt.Update(msg)

	return m, cmd
}

// submitNewThread creates the thread and returns to whatever screen opened
// the form. An empty prompt is not an error: it creates an idle thread the
// user can talk to, which is what the form's own placeholder implies.
func (m Model) submitNewThread() (Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.form.prompt.Value())
	parent := m.form.parent

	m.popOrClose()

	if m.client == nil {
		return m, nil
	}

	return m, m.client.newThread(prompt, "", parent, nil)
}

func (m Model) renderNewThread() string {
	title := "new thread"
	if m.form.parent != "" {
		title = "fork · inherits the change set, not the transcript"
	}

	body := []string{
		"",
		"> " + m.form.prompt.View(),
		"",
	}

	return frame(m.width, title, body, footerHints(newThreadHints(), m.width-boxPad), m.th)
}

func newThreadHints() []hint {
	return []hint{
		{keyEnter, "create"},
		{keyEsc, "cancel"},
	}
}
