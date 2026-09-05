package thread_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/thread"
)

func toolMsg(turn int, content string) thread.TurnMessage {
	return thread.TurnMessage{Message: llm.Message{Role: llm.RoleTool, Content: content}, Turn: turn}
}

func TestTruncateToolOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		keepLines  int
		wantSaved  bool
		wantChange bool
	}{
		{
			name:       "over threshold is truncated",
			content:    strings.Repeat("a line of tool output\n", 40),
			keepLines:  5,
			wantSaved:  true,
			wantChange: true,
		},
		{
			name:       "under threshold is untouched",
			content:    "line1\nline2\nline3",
			keepLines:  5,
			wantSaved:  false,
			wantChange: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items := []thread.TurnMessage{toolMsg(1, tt.content)}
			out, savings := thread.TruncateToolOutput(items, tt.keepLines)

			if (savings.TokensSaved > 0) != tt.wantSaved {
				t.Errorf("TokensSaved = %d, wantSaved = %v", savings.TokensSaved, tt.wantSaved)
			}
			if (savings.ItemsChanged > 0) != tt.wantChange {
				t.Errorf("ItemsChanged = %d, wantChange = %v", savings.ItemsChanged, tt.wantChange)
			}
			if tt.wantChange {
				if !strings.Contains(out[0].Message.Content, "lines omitted") {
					t.Errorf("truncated content = %q, want an omission marker", out[0].Message.Content)
				}
			} else if out[0].Message.Content != tt.content {
				t.Errorf("content = %q, want unchanged %q", out[0].Message.Content, tt.content)
			}

			// The source slice must never be mutated.
			if items[0].Message.Content != tt.content {
				t.Errorf("source mutated: %q", items[0].Message.Content)
			}
		})
	}
}

func TestMaskOldToolResults(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("some tool result content here, spanning several sentences. ", 5)
	one := len(body) / 4

	tests := []struct {
		name       string
		wantMasked []int
		budget     int
	}{
		{name: "budget holds everything", budget: one * 3, wantMasked: nil},
		{name: "budget holds the two newest", budget: one * 2, wantMasked: []int{0}},
		{name: "budget holds only the newest", budget: one, wantMasked: []int{0, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items := []thread.TurnMessage{toolMsg(1, body), toolMsg(2, body), toolMsg(3, body)}
			out, savings := thread.MaskOldToolResults(items, tt.budget)

			if savings.ItemsChanged != len(tt.wantMasked) {
				t.Errorf("ItemsChanged = %d, want %d", savings.ItemsChanged, len(tt.wantMasked))
			}
			masked := map[int]bool{}
			for _, i := range tt.wantMasked {
				masked[i] = true
			}
			for i, item := range out {
				got := strings.Contains(item.Message.Content, "omitted")
				if got != masked[i] {
					t.Errorf("item %d masked = %v, want %v", i, got, masked[i])
				}
			}
			if items[0].Message.Content != body {
				t.Error("source mutated")
			}
		})
	}
}

// The newest result is kept whatever it costs, because a request whose latest
// observation has been masked is one the model cannot act on at all.
func TestMaskOldToolResults_KeepsTheNewestPastTheBudget(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 40000)
	items := []thread.TurnMessage{toolMsg(1, big), toolMsg(2, big)}

	out, savings := thread.MaskOldToolResults(items, 1)

	if savings.ItemsChanged != 1 {
		t.Fatalf("ItemsChanged = %d, want 1", savings.ItemsChanged)
	}
	if out[1].Message.Content != big {
		t.Error("the newest tool result was masked")
	}
}

func TestDedupeToolReads(t *testing.T) {
	t.Parallel()

	const fileContent = "package main\n\nimport \"fmt\"\n\n" +
		"func main() {\n\tfmt.Println(\"hello, world, this is a longer file body\")\n}\n"

	items := []thread.TurnMessage{
		toolMsg(1, fileContent),
		toolMsg(2, "unrelated content"),
		toolMsg(3, fileContent),
	}

	out, savings := thread.DedupeToolReads(items)

	if savings.ItemsChanged != 1 {
		t.Fatalf("ItemsChanged = %d, want 1", savings.ItemsChanged)
	}
	if savings.TokensSaved <= 0 {
		t.Errorf("TokensSaved = %d, want > 0", savings.TokensSaved)
	}
	if out[0].Message.Content != items[0].Message.Content {
		t.Errorf("first occurrence changed: %q", out[0].Message.Content)
	}
	if !strings.Contains(out[2].Message.Content, "same content as turn 1") {
		t.Errorf("third item = %q, want a reference to turn 1", out[2].Message.Content)
	}
	if items[2].Message.Content == out[2].Message.Content {
		t.Error("source item mutated")
	}
}

func TestCompactRunsEnabledRulesOnly(t *testing.T) {
	t.Parallel()

	items := []thread.TurnMessage{
		toolMsg(1, strings.Repeat("output line\n", 40)),
	}

	out, report := thread.Compact(items, thread.CompactOptions{})
	if len(report.Rules) != 0 {
		t.Errorf("Rules = %v, want none run with zero-value options", report.Rules)
	}
	if out[0].Message.Content != items[0].Message.Content {
		t.Error("Compact with no rules enabled must not change content")
	}

	out, report = thread.Compact(items, thread.CompactOptions{KeepLines: 5})
	if _, ok := report.Rules["truncate_tool_output"]; !ok {
		t.Fatalf("Rules = %v, want truncate_tool_output", report.Rules)
	}
	if report.TotalTokens <= 0 {
		t.Errorf("TotalTokens = %d, want > 0", report.TotalTokens)
	}
	if strings.EqualFold(out[0].Message.Content, items[0].Message.Content) {
		t.Error("Compact with KeepLines set must truncate content")
	}
}

func TestFlatten(t *testing.T) {
	t.Parallel()

	items := []thread.TurnMessage{
		{Message: llm.Message{Role: llm.RoleUser, Content: "hi"}, Turn: 0},
		toolMsg(1, "result"),
	}

	got := thread.Flatten(items)
	if len(got) != 2 || got[0].Content != "hi" || got[1].Content != "result" {
		t.Errorf("Flatten = %+v", got)
	}
}
