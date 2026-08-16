package tools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

var errNoUserAttached = errors.New("no user attached")

type fakeAsker struct {
	err    error
	answer string
}

func (f fakeAsker) Ask(context.Context, string) (string, error) { return f.answer, f.err }

func TestQuestion_ReturnsTheAskerAnswer(t *testing.T) {
	t.Parallel()

	q := tools.NewQuestion(fakeAsker{answer: "use tabs"})
	result, err := q.Run(context.Background(), mustJSON(t, map[string]any{"question": "tabs or spaces?"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}
	if result.Content != "use tabs" {
		t.Errorf("Content = %q, want %q", result.Content, "use tabs")
	}
}

func TestQuestion_AskerErrorIsAnErrorResult(t *testing.T) {
	t.Parallel()

	q := tools.NewQuestion(fakeAsker{err: errNoUserAttached})
	result, err := q.Run(context.Background(), mustJSON(t, map[string]any{"question": "tabs or spaces?"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true when the asker fails")
	}
}

func TestQuestion_RequiresQuestion(t *testing.T) {
	t.Parallel()

	q := tools.NewQuestion(fakeAsker{answer: "unused"})
	result, err := q.Run(context.Background(), mustJSON(t, map[string]any{"question": ""}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for an empty question")
	}
}
