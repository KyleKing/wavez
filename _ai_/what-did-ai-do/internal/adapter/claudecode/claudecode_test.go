package claudecode_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/adapter/claudecode"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

func parseSimpleSession(t *testing.T) session.Session {
	t.Helper()

	sess, err := claudecode.Parse("testdata/simple_session.jsonl")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	return sess
}

func TestParse_SimpleSession_Metadata(t *testing.T) {
	t.Parallel()

	sess := parseSimpleSession(t)

	if sess.ID != "test-session-1" {
		t.Errorf("ID = %q, want %q", sess.ID, "test-session-1")
	}

	if sess.Agent != session.AgentClaudeCode {
		t.Errorf("Agent = %q, want %q", sess.Agent, session.AgentClaudeCode)
	}

	if sess.ProjectPath != "/repo" {
		t.Errorf("ProjectPath = %q, want %q", sess.ProjectPath, "/repo")
	}

	wantStart := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if !sess.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", sess.StartedAt, wantStart)
	}
}

func TestParse_SimpleSession_ToolCalls(t *testing.T) {
	t.Parallel()

	sess := parseSimpleSession(t)

	if len(sess.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(sess.ToolCalls))
	}

	edit := sess.ToolCalls[0]
	if edit.Name != "Edit" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", edit.Name, "Edit")
	}

	if len(edit.Files) != 1 || edit.Files[0] != "/repo/parser.go" {
		t.Errorf("ToolCalls[0].Files = %v, want [/repo/parser.go]", edit.Files)
	}

	if edit.Output != "The file /repo/parser.go has been updated." {
		t.Errorf("ToolCalls[0].Output = %q, unexpected", edit.Output)
	}

	bash := sess.ToolCalls[1]
	if bash.Name != "Bash" {
		t.Errorf("ToolCalls[1].Name = %q, want %q", bash.Name, "Bash")
	}

	if len(bash.Files) != 0 {
		t.Errorf("ToolCalls[1].Files = %v, want none", bash.Files)
	}
}

func TestParse_SimpleSession_Messages(t *testing.T) {
	t.Parallel()

	sess := parseSimpleSession(t)

	var userMsgs, assistantMsgs int

	for _, m := range sess.Messages {
		switch m.Role {
		case "user":
			userMsgs++
		case "assistant":
			assistantMsgs++
		}
	}

	if userMsgs != 1 {
		t.Errorf("user messages = %d, want 1", userMsgs)
	}

	if assistantMsgs != 1 {
		t.Errorf(
			"assistant messages = %d, want 1 (tool_use-only turns produce no message text)",
			assistantMsgs,
		)
	}
}

func TestParse_MalformedTrailingLine(t *testing.T) {
	t.Parallel()

	sess, err := claudecode.Parse("testdata/malformed_trailing.jsonl")
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil (malformed trailing line should be skipped)", err)
	}

	if sess.ID != "test-session-2" {
		t.Errorf("ID = %q, want %q", sess.ID, "test-session-2")
	}

	if len(sess.Messages) != 2 {
		t.Errorf("len(Messages) = %d, want 2 (trailing malformed line skipped)", len(sess.Messages))
	}
}

func TestParse_MissingFile(t *testing.T) {
	t.Parallel()

	if _, err := claudecode.Parse("testdata/does_not_exist.jsonl"); err == nil {
		t.Error("Parse() error = nil, want error for missing file")
	}
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projDir := filepath.Join(dir, "-Users-test-repo")

	if err := os.MkdirAll(projDir, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	sessionFile := filepath.Join(projDir, "abc123.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(projDir, "notes.txt"),
		[]byte("ignore me"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := claudecode.Discover(dir)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(got) != 1 || got[0] != sessionFile {
		t.Errorf("Discover() = %v, want [%s]", got, sessionFile)
	}
}

func TestDiscover_MissingDir(t *testing.T) {
	t.Parallel()

	got, err := claudecode.Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil for a missing projects dir", err)
	}

	if len(got) != 0 {
		t.Errorf("Discover() = %v, want empty", got)
	}
}
