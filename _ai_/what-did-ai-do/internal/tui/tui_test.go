package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/what-did-ai-do/internal/session"
	"github.com/kyleking/what-did-ai-do/internal/tui"
)

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{Text: s, Code: rune(s[0])}
	}
}

func quizWorthySession() session.Session {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	return session.Session{
		ID:    "sess-1",
		Agent: session.AgentClaudeCode,
		Messages: []session.Message{
			{
				At:   start,
				Role: "assistant",
				Text: "Switching to strconv.ParseInt since the regex silently drops leading zeros.",
			},
		},
		ToolCalls: []session.ToolCall{
			{At: start.Add(time.Second), Name: "Edit", Files: []string{"parser.go"}},
			{At: start.Add(2 * time.Second), Name: "Bash", Input: `{"command":"go test ./..."}`},
		},
	}
}

func TestModel_View_EmptySessionsDoesNotPanic(t *testing.T) {
	t.Parallel()

	m := tui.New(nil)
	if out := m.View().Content; out == "" {
		t.Error("View().Content is empty, want a rendered (if empty) session list")
	}
}

func TestModel_SelectSession_EntersQuiz(t *testing.T) {
	t.Parallel()

	m := tui.New([]session.Session{quizWorthySession()})

	updated, _ := m.Update(keyPress("enter"))

	view := updated.View().Content
	if !strings.Contains(view, "Question 1/") {
		t.Errorf(
			"View() after selecting a quiz-worthy session = %q, want it to contain %q",
			view,
			"Question 1/",
		)
	}
}

func TestModel_SelectSession_NoDecisionsStaysOnList(t *testing.T) {
	t.Parallel()

	empty := session.Session{ID: "sess-empty", Agent: session.AgentClaudeCode}
	m := tui.New([]session.Session{empty})

	updated, _ := m.Update(keyPress("enter"))

	view := updated.View().Content
	if strings.Contains(view, "Question 1/") {
		t.Errorf(
			"View() after selecting an empty session = %q, want to remain on the session list",
			view,
		)
	}
}

func TestModel_AnswerQuestion_ShowsFeedbackThenAdvances(t *testing.T) {
	t.Parallel()

	m := tui.New([]session.Session{quizWorthySession()})

	updated, _ := m.Update(keyPress("enter"))

	updated, _ = updated.Update(keyPress("1"))

	view := updated.View().Content
	if !strings.Contains(view, "correct") {
		t.Errorf("View() after answering = %q, want feedback containing %q", view, "correct")
	}

	updated, _ = updated.Update(keyPress("n"))

	view = updated.View().Content
	if strings.Contains(view, "Question 1/") {
		t.Errorf("View() after advancing = %q, want to have left question 1", view)
	}
}

func TestModel_EscFromQuiz_ReturnsToSessionList(t *testing.T) {
	t.Parallel()

	m := tui.New([]session.Session{quizWorthySession()})

	updated, _ := m.Update(keyPress("enter"))
	if !strings.Contains(updated.View().Content, "Question 1/") {
		t.Fatalf(
			"View() = %q, expected to be in the quiz before testing esc",
			updated.View().Content,
		)
	}

	updated, _ = updated.Update(keyPress("esc"))

	if strings.Contains(updated.View().Content, "Question 1/") {
		t.Error("View() after esc still shows the quiz, want the session list")
	}
}

func TestModel_QuitFromSessionList(t *testing.T) {
	t.Parallel()

	m := tui.New(nil)

	_, cmd := m.Update(keyPress("q"))
	if cmd == nil {
		t.Error("Update(q) on the session list returned a nil Cmd, want tea.Quit")
	}
}
