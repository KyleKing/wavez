package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kyleking/what-did-ai-do/internal/quiz"
)

// quizScreen holds the state of an in-progress (or just-finished) quiz run
// over a single session's generated questions.
type quizScreen struct {
	questions []quiz.Question
	index     int
	score     int
	// picked is the choice index the user selected for the current
	// question, or -1 before they've answered it.
	picked int
}

func newQuizScreen(questions []quiz.Question) quizScreen {
	return quizScreen{questions: questions, picked: -1}
}

func (q quizScreen) done() bool {
	return q.index >= len(q.questions)
}

func (q quizScreen) current() quiz.Question {
	return q.questions[q.index]
}

func (q quizScreen) answered() bool {
	return q.picked >= 0
}

// answer records the user's choice for the current question and updates
// the running score; it's a no-op once the question has been answered.
func (q quizScreen) answer(choice int) quizScreen {
	if q.answered() || choice < 0 || choice >= len(q.current().Choices) {
		return q
	}

	q.picked = choice
	if choice == q.current().AnswerIndex {
		q.score++
	}

	return q
}

func (q quizScreen) next() quizScreen {
	if !q.answered() {
		return q
	}

	q.index++
	q.picked = -1

	return q
}

func renderQuiz(q quizScreen) string {
	if q.done() {
		return renderQuizResult(q)
	}

	cur := q.current()

	var b strings.Builder

	fmt.Fprintf(&b, "Question %d/%d\n\n%s\n\n", q.index+1, len(q.questions), cur.Prompt)

	for i, choice := range cur.Choices {
		b.WriteString(renderChoice(q, i, choice))
	}

	if q.answered() {
		b.WriteString("\npress n for next question, q to quit\n")
	} else {
		b.WriteString("\npress 1-4 to answer, q to quit\n")
	}

	return b.String()
}

func renderChoice(q quizScreen, i int, choice string) string {
	prefix := fmt.Sprintf("  %d. ", i+1)

	if !q.answered() {
		return prefix + choice + "\n"
	}

	switch {
	case i == q.current().AnswerIndex:
		return prefix + choice + "  ✓ correct\n"
	case i == q.picked:
		return prefix + choice + "  ✗ your answer\n"
	default:
		return prefix + choice + "\n"
	}
}

func renderQuizResult(q quizScreen) string {
	return fmt.Sprintf(
		"Quiz complete: %d/%d correct\n\npress enter to return to sessions, q to quit\n",
		q.score, len(q.questions),
	)
}

// parseAnswerKey maps a "1".."9" keypress to a zero-based choice index.
func parseAnswerKey(key string) (int, bool) {
	n, err := strconv.Atoi(key)
	if err != nil || n < 1 {
		return 0, false
	}

	return n - 1, true
}
