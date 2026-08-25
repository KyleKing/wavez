package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/thread"
)

func call(turn int, id, name string) thread.TurnMessage {
	return thread.TurnMessage{
		Turn: turn,
		Message: llm.Message{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: id, Name: name, Input: json.RawMessage(`{}`)}},
		},
	}
}

func answer(turn int, id, content string, isError bool) thread.TurnMessage {
	return thread.TurnMessage{
		Turn:    turn,
		Message: llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: content, IsError: isError},
	}
}

func TestRecordedCallsPairEachCallWithItsAnswer(t *testing.T) {
	t.Parallel()

	got := recordedCalls([]thread.TurnMessage{
		call(1, "a", "read"),
		answer(1, "a", "ok", false),
		call(2, "b", "str_replace"),
		answer(2, "b", "old_string not found", true),
		// A run killed mid-call leaves the last one unanswered, which is
		// the transcript worth opening rather than one to refuse.
		call(3, "c", "shell"),
	})

	if len(got) != 3 {
		t.Fatalf("recordedCalls returned %d calls, want 3", len(got))
	}

	if got[1].Answer != "old_string not found" || !got[1].IsError {
		t.Errorf("call 2 carries %q (error %v), want the failure it was answered with",
			got[1].Answer, got[1].IsError)
	}

	if got[2].Answer != "" || got[2].IsError {
		t.Errorf("the unanswered call carries %q, want nothing", got[2].Answer)
	}
}

func TestSelectRecalled(t *testing.T) {
	t.Parallel()

	calls := recordedCalls([]thread.TurnMessage{
		call(1, "a", "read"),
		answer(1, "a", "ok", false),
		call(4, "b", "str_replace"),
		answer(4, "b", "old_string not found", true),
	})

	tests := []struct {
		name string
		turn int
		want int
		fail bool
	}{
		{name: "no turn takes the first failure", turn: 0, want: 1},
		{name: "a named turn takes that call", turn: 1, want: 0},
		{name: "a turn that made no call is refused", turn: 3, fail: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := selectRecalled(calls, tt.turn)
			if tt.fail {
				if !errors.Is(err, errNoRecordedCall) {
					t.Fatalf("selectRecalled(%d) = %v, want errNoRecordedCall", tt.turn, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("selectRecalled(%d): %v", tt.turn, err)
			}

			if got != tt.want {
				t.Errorf("selectRecalled(%d) = %d, want %d", tt.turn, got, tt.want)
			}
		})
	}
}
