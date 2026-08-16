// Package tui implements the Bubble Tea v2 terminal UI: a session list over
// all local agent sessions, feeding into a per-session quiz flow.
package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/what-did-ai-do/internal/extract"
	"github.com/kyleking/what-did-ai-do/internal/findingsstore"
	"github.com/kyleking/what-did-ai-do/internal/quiz"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// screen identifies which view the root Model is currently showing.
type screen int

const (
	screenSessions screen = iota
	screenQuiz
	screenFindings
)

const (
	defaultListWidth  = 80
	defaultListHeight = 20
)

// Model is the root Bubble Tea model for the application. Its methods use
// pointer receivers: list.Model embeds enough state that copying it on
// every Update would be wasteful, and Bubble Tea's Model interface allows
// either receiver style.
type Model struct {
	list     list.Model
	findings string
	quiz     quizScreen
	screen   screen
	quitting bool
}

// New returns the initial application model listing the given sessions.
func New(sessions []session.Session) *Model {
	items := make([]list.Item, len(sessions))

	for i := range sessions {
		items[i] = &sessionItem{session: sessions[i]}
	}

	l := list.New(items, list.NewDefaultDelegate(), defaultListWidth, defaultListHeight)
	l.Title = "what-did-ai-do — sessions"

	return &Model{list: l}
}

// Init satisfies tea.Model; no initial command is needed.
func (*Model) Init() tea.Cmd {
	return nil
}

// Update satisfies tea.Model.
//
//nolint:ireturn // tea.Model.Update is required to return the tea.Model interface
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "q":
			if m.screen == screenSessions {
				m.quitting = true
				return m, tea.Quit
			}
		}
	}

	switch m.screen {
	case screenSessions:
		return m.updateSessions(msg)
	case screenQuiz:
		return m.updateQuiz(msg)
	case screenFindings:
		return m.updateFindings(msg)
	default:
		return m, nil
	}
}

//nolint:ireturn // see Update
func (m *Model) updateSessions(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		item, hasSelection := m.list.SelectedItem().(*sessionItem)

		switch keyMsg.String() {
		case "enter":
			if hasSelection {
				return m.enterQuiz(item)
			}
		case "f":
			if hasSelection {
				return m.enterFindings(item)
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)

	return m, cmd
}

//nolint:ireturn // see Update
func (m *Model) enterQuiz(item *sessionItem) (tea.Model, tea.Cmd) {
	questions := generateQuiz(&item.session)
	if len(questions) == 0 {
		return m, m.list.NewStatusMessage("no quiz-worthy decisions found in this session")
	}

	m.quiz = newQuizScreen(questions)
	m.screen = screenQuiz

	return m, nil
}

//nolint:ireturn // see Update
func (m *Model) enterFindings(item *sessionItem) (tea.Model, tea.Cmd) {
	report, found, err := findingsstore.Load(item.session.ID)
	if err != nil || !found {
		return m, m.list.NewStatusMessage("no adversarial review yet — run `what-did-ai-do review` first")
	}

	m.findings = findingsstore.Render(report)
	m.screen = screenFindings

	return m, nil
}

//nolint:ireturn // see Update
func (m *Model) updateFindings(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && keyMsg.String() == "esc" {
		m.screen = screenSessions
	}

	return m, nil
}

//nolint:ireturn // see Update
func (m *Model) updateQuiz(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	key := keyMsg.String()

	switch {
	case key == "esc":
		m.screen = screenSessions
		return m, nil
	case m.quiz.done() && key == "enter":
		m.screen = screenSessions
		return m, nil
	case !m.quiz.done() && !m.quiz.answered():
		if choice, ok := parseAnswerKey(key); ok {
			m.quiz = m.quiz.answer(choice)
		}
	case !m.quiz.done() && m.quiz.answered() && key == "n":
		m.quiz = m.quiz.next()
	}

	return m, nil
}

// View satisfies tea.Model.
func (m *Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	switch m.screen {
	case screenQuiz:
		return tea.NewView(renderQuiz(m.quiz))
	case screenFindings:
		return tea.NewView(m.findings + "\npress esc to return to sessions\n")
	default:
		return tea.NewView(m.list.View())
	}
}

// generateQuiz runs the heuristic decision extractor and quiz generator for
// a single session. LLM-backed rationale extraction is a documented v2
// feature (see DESIGN.md); this MVP path is heuristic-only.
func generateQuiz(s *session.Session) []quiz.Question {
	return quiz.Generate(extract.Extract(*s))
}
