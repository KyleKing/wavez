package aider_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/adapter/aider"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

func TestDiscover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setupDir  func(t *testing.T) string
		name      string
		wantEmpty bool
	}{
		{
			name:      "history file present",
			setupDir:  setupDirWithHistory,
			wantEmpty: false,
		},
		{
			name:      "no history file",
			setupDir:  func(t *testing.T) string { t.Helper(); return t.TempDir() },
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := tt.setupDir(t)

			paths, err := aider.Discover(dir)
			if err != nil {
				t.Fatalf("Discover(%q) returned error: %v", dir, err)
			}

			if tt.wantEmpty && len(paths) != 0 {
				t.Errorf("Discover(%q) = %v; want empty", dir, paths)
			}

			if !tt.wantEmpty && len(paths) != 1 {
				t.Errorf("Discover(%q) = %v; want exactly one path", dir, paths)
			}
		})
	}
}

func setupDirWithHistory(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	historyPath := filepath.Join(dir, ".aider.chat.history.md")
	content := []byte("# aider chat started at 2026-07-10 14:32:11\n")

	if err := os.WriteFile(historyPath, content, 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	return dir
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		fixture           string
		wantToolCallCount []int
		wantMessageCount  []int
		wantSessionCount  int
	}{
		{
			name:              "single session with one edit",
			fixture:           "single_session.md",
			wantSessionCount:  1,
			wantToolCallCount: []int{1},
			wantMessageCount:  []int{2},
		},
		{
			name:              "multiple sessions in one file",
			fixture:           "multi_session.md",
			wantSessionCount:  2,
			wantToolCallCount: []int{1, 1},
			wantMessageCount:  []int{2, 2},
		},
		{
			name:              "session with multiple diff blocks",
			fixture:           "multi_edit_session.md",
			wantSessionCount:  1,
			wantToolCallCount: []int{2},
			wantMessageCount:  []int{2},
		},
		{
			name:              "malformed trailing block is dropped, not fatal",
			fixture:           "malformed_trailing.md",
			wantSessionCount:  2,
			wantToolCallCount: []int{1, 0},
			wantMessageCount:  []int{2, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("testdata", tt.fixture)

			sessions, err := aider.Parse(path)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", path, err)
			}

			if len(sessions) != tt.wantSessionCount {
				t.Fatalf(
					"Parse(%q) returned %d sessions; want %d",
					path,
					len(sessions),
					tt.wantSessionCount,
				)
			}

			for i := range sessions {
				checkSessionShape(
					t,
					i,
					&sessions[i],
					tt.wantToolCallCount[i],
					tt.wantMessageCount[i],
				)
			}
		})
	}
}

func checkSessionShape(
	t *testing.T,
	i int,
	sess *session.Session,
	wantToolCalls, wantMessages int,
) {
	t.Helper()

	if sess.Agent != session.AgentAider {
		t.Errorf("session %d Agent = %q; want %q", i, sess.Agent, session.AgentAider)
	}

	if sess.ID == "" {
		t.Errorf("session %d ID is empty", i)
	}

	if sess.StartedAt.IsZero() {
		t.Errorf("session %d StartedAt is zero", i)
	}

	if len(sess.ToolCalls) != wantToolCalls {
		t.Errorf("session %d has %d tool calls; want %d", i, len(sess.ToolCalls), wantToolCalls)
	}

	if len(sess.Messages) != wantMessages {
		t.Errorf("session %d has %d messages; want %d", i, len(sess.Messages), wantMessages)
	}
}

func TestParse_SingleSessionDetails(t *testing.T) {
	t.Parallel()

	sessions, err := aider.Parse(filepath.Join("testdata", "single_session.md"))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("got %d sessions; want 1", len(sessions))
	}

	sess := &sessions[0]

	checkSessionMetadata(t, sess)
	checkSessionMessages(t, sess)
	checkSessionToolCalls(t, sess)
}

func checkSessionMetadata(t *testing.T, sess *session.Session) {
	t.Helper()

	wantStartedAt := time.Date(2026, 7, 10, 14, 32, 11, 0, time.UTC)
	if !sess.StartedAt.Equal(wantStartedAt) {
		t.Errorf("StartedAt = %v; want %v", sess.StartedAt, wantStartedAt)
	}

	if sess.ID != "2026-07-10-14-32-11" {
		t.Errorf("ID = %q; want %q", sess.ID, "2026-07-10-14-32-11")
	}

	wantProjectPath, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("resolving testdata path: %v", err)
	}

	gotProjectPath, err := filepath.Abs(sess.ProjectPath)
	if err != nil {
		t.Fatalf("resolving session project path: %v", err)
	}

	if gotProjectPath != wantProjectPath {
		t.Errorf("ProjectPath = %q; want %q", gotProjectPath, wantProjectPath)
	}
}

func checkSessionMessages(t *testing.T, sess *session.Session) {
	t.Helper()

	if len(sess.Messages) != 2 {
		t.Fatalf("got %d messages; want 2", len(sess.Messages))
	}

	if sess.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q; want %q", sess.Messages[0].Role, "user")
	}

	wantUserText := "Add error wrapping to the Parse function"
	if sess.Messages[0].Text != wantUserText {
		t.Errorf("Messages[0].Text = %q; want %q", sess.Messages[0].Text, wantUserText)
	}

	if sess.Messages[1].Role != "assistant" {
		t.Errorf("Messages[1].Role = %q; want %q", sess.Messages[1].Role, "assistant")
	}

	wantAssistantText := "I added error wrapping so callers can use errors.Is/errors.As on failures."
	if sess.Messages[1].Text != wantAssistantText {
		t.Errorf("Messages[1].Text = %q; want %q", sess.Messages[1].Text, wantAssistantText)
	}
}

func checkSessionToolCalls(t *testing.T, sess *session.Session) {
	t.Helper()

	if len(sess.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls; want 1", len(sess.ToolCalls))
	}

	toolCall := sess.ToolCalls[0]
	if toolCall.Name != "aider-edit" {
		t.Errorf("ToolCalls[0].Name = %q; want %q", toolCall.Name, "aider-edit")
	}

	if len(toolCall.Files) != 1 || toolCall.Files[0] != "internal/foo/foo.go" {
		t.Errorf("ToolCalls[0].Files = %v; want [internal/foo/foo.go]", toolCall.Files)
	}

	if toolCall.Input == "" || toolCall.Output == "" {
		t.Errorf("ToolCalls[0] Input/Output should not be empty")
	}
}

func TestParse_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := aider.Parse(filepath.Join("testdata", "does-not-exist.md"))
	if err == nil {
		t.Fatal("Parse of a missing file should return an error")
	}
}
