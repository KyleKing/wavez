package extract_test

import (
	"testing"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/decision"
	"github.com/kyleking/what-did-ai-do/internal/extract"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

func at(seconds int) time.Time {
	return time.Date(2026, 7, 18, 12, 0, seconds, 0, time.UTC)
}

func TestExtract_Rationale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		wantSource        decision.Source
		messages          []session.Message
		wantRationaleZero bool
	}{
		{
			name: "long preceding assistant message yields transcript rationale",
			messages: []session.Message{
				{
					At:   at(0),
					Role: "assistant",
					Text: "I'm going to update the config loader to handle missing env vars so startup doesn't panic.",
				},
			},
			wantSource:        decision.SourceTranscript,
			wantRationaleZero: false,
		},
		{
			name:              "no nearby assistant text yields structural source",
			messages:          nil,
			wantSource:        decision.SourceStructural,
			wantRationaleZero: true,
		},
		{
			name: "only trivial nearby text yields structural source",
			messages: []session.Message{
				{At: at(0), Role: "assistant", Text: "Let me check that."},
			},
			wantSource:        decision.SourceStructural,
			wantRationaleZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := session.Session{
				ID:       "sess1",
				Messages: tt.messages,
				ToolCalls: []session.ToolCall{
					{
						At:    at(1),
						Name:  "Edit",
						Input: "...",
						Files: []string{"internal/config/config.go"},
					},
				},
			}

			got := extract.Extract(s)
			if len(got) != 1 {
				t.Fatalf("len(got) = %d; want 1", len(got))
			}

			d := got[0]
			if d.Source != tt.wantSource {
				t.Errorf("Source = %v; want %v", d.Source, tt.wantSource)
			}

			if (d.Rationale == "") != tt.wantRationaleZero {
				t.Errorf("Rationale = %q; want empty=%v", d.Rationale, tt.wantRationaleZero)
			}
		})
	}
}

func TestExtract_Grouping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		toolCalls []session.ToolCall
		wantFiles [][]string
	}{
		{
			name: "consecutive tool calls on same file merge into one decision",
			toolCalls: []session.ToolCall{
				{At: at(1), Name: "Edit", Input: "edit 1", Files: []string{"internal/foo/foo.go"}},
				{At: at(2), Name: "Edit", Input: "edit 2", Files: []string{"internal/foo/foo.go"}},
				{At: at(3), Name: "Edit", Input: "edit 3", Files: []string{"internal/foo/foo.go"}},
			},
			wantFiles: [][]string{{"internal/foo/foo.go"}},
		},
		{
			name: "tool calls on different files produce separate chronological decisions",
			toolCalls: []session.ToolCall{
				{At: at(1), Name: "Edit", Input: "edit a", Files: []string{"a.go"}},
				{At: at(2), Name: "Edit", Input: "edit b", Files: []string{"b.go"}},
			},
			wantFiles: [][]string{{"a.go"}, {"b.go"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := extract.Extract(session.Session{ID: "sess-group", ToolCalls: tt.toolCalls})
			if len(got) != len(tt.wantFiles) {
				t.Fatalf("len(got) = %d; want %d", len(got), len(tt.wantFiles))
			}

			for i, want := range tt.wantFiles {
				if len(got[i].Files) != len(want) || got[i].Files[0] != want[0] {
					t.Errorf("decision %d Files = %v; want %v", i, got[i].Files, want)
				}
			}
		})
	}
}

func TestExtract_NoFilesToolCall(t *testing.T) {
	t.Parallel()

	got := extract.Extract(session.Session{
		ID: "sess5",
		ToolCalls: []session.ToolCall{
			{At: at(1), Name: "Bash", Input: "go test ./...", Files: nil},
		},
	})

	if len(got) != 1 {
		t.Fatalf("len(got) = %d; want 1", len(got))
	}

	if len(got[0].Files) != 0 {
		t.Errorf("Files = %v; want empty", got[0].Files)
	}

	if len(got[0].ToolNames) != 1 || got[0].ToolNames[0] != "Bash" {
		t.Errorf("ToolNames = %v; want [Bash]", got[0].ToolNames)
	}
}

func TestExtract_NoFilesToolCall_JSONInputDecodedForSummary(t *testing.T) {
	t.Parallel()

	got := extract.Extract(session.Session{
		ID: "sess-json",
		ToolCalls: []session.ToolCall{
			{
				At:    at(1),
				Name:  "Bash",
				Input: `{"command":"go test ./...","description":"run tests"}`,
			},
		},
	})

	if len(got) != 1 {
		t.Fatalf("len(got) = %d; want 1", len(got))
	}

	want := "ran Bash: go test ./..."
	if got[0].Summary != want {
		t.Errorf("Summary = %q; want %q", got[0].Summary, want)
	}
}

func TestExtract_EmptySession(t *testing.T) {
	t.Parallel()

	got := extract.Extract(session.Session{ID: "sess6"})
	if got == nil {
		t.Fatal("got nil; want non-nil empty slice")
	}

	if len(got) != 0 {
		t.Errorf("len(got) = %d; want 0", len(got))
	}
}

func TestExtract_IDsDeterministicAndUnique(t *testing.T) {
	t.Parallel()

	s := session.Session{
		ID: "sess7",
		ToolCalls: []session.ToolCall{
			{At: at(1), Name: "Edit", Input: "edit a", Files: []string{"a.go"}},
			{At: at(2), Name: "Bash", Input: "go build ./...", Files: nil},
			{At: at(3), Name: "Edit", Input: "edit b", Files: []string{"b.go"}},
		},
	}

	got := extract.Extract(s)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d; want 3", len(got))
	}

	seen := make(map[string]bool)
	for _, d := range got {
		if d.ID == "" {
			t.Error("ID is empty")
		}

		if seen[d.ID] {
			t.Errorf("duplicate ID %q", d.ID)
		}

		seen[d.ID] = true
	}

	got2 := extract.Extract(s)
	for i := range got {
		if got[i].ID != got2[i].ID {
			t.Errorf("ID not deterministic: %q != %q", got[i].ID, got2[i].ID)
		}
	}
}
