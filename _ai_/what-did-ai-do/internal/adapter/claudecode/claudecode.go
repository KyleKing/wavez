// Package claudecode parses Claude Code's local JSONL session transcripts
// (one file per session, under ~/.claude/projects/<slug>/*.jsonl) into the
// agent-agnostic session.Session IR.
package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/session"
)

// DefaultProjectsDir returns ~/.claude/projects, where Claude Code stores
// one subdirectory per project (slugified from its absolute path) and one
// JSONL file per session within it.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}

	return filepath.Join(home, ".claude", "projects"), nil
}

// Discover returns every session JSONL file found under projectsDir
// (recursively, across all projects).
func Discover(projectsDir string) ([]string, error) {
	var paths []string

	err := filepath.WalkDir(projectsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return err
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			paths = append(paths, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", projectsDir, err)
	}

	return paths, nil
}

// rawEntry is the subset of a Claude Code JSONL line this adapter cares
// about; every other line type (mode, permission-mode, bridge-session, ...)
// Is skipped.
//
//nolint:tagliatelle // field names mirror Claude Code's own camelCase wire format
type rawEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"sessionId"`
	Cwd       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type rawContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// initialScanBufSize and maxScanBufSize bound bufio.Scanner's line buffer;
// Claude Code transcript lines can carry large tool_use inputs/outputs, well
// past the default 64KiB scanner token limit.
const (
	initialScanBufSize = 64 * 1024
	maxScanBufSize     = 10 * 1024 * 1024
)

// Parse reads a single Claude Code session JSONL file into a session.Session.
//
//nolint:gosec // path is caller-supplied (a discovered transcript file), not attacker-controlled input
func Parse(path string) (session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Session{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() {
		//nolint:errcheck // read-only file handle; a close error here can't affect the already-read data
		f.Close()
	}()

	sess := session.Session{Agent: session.AgentClaudeCode}
	toolCallByID := map[string]*session.ToolCall{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, initialScanBufSize), maxScanBufSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry rawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // tolerate malformed/partial lines rather than failing the whole file
		}

		if sess.ID == "" && entry.SessionID != "" {
			sess.ID = entry.SessionID
		}

		if sess.ProjectPath == "" && entry.Cwd != "" {
			sess.ProjectPath = entry.Cwd
		}

		at := parseTimestamp(entry.Timestamp)
		if sess.StartedAt.IsZero() && !at.IsZero() {
			sess.StartedAt = at
		}

		switch entry.Type {
		case "user":
			applyUserEntry(&sess, entry.Message, at, toolCallByID)
		case "assistant":
			applyAssistantEntry(&sess, entry.Message, at, toolCallByID)
		}
	}

	if err := scanner.Err(); err != nil {
		return session.Session{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return sess, nil
}

func applyUserEntry(
	sess *session.Session,
	raw json.RawMessage,
	at time.Time,
	toolCallByID map[string]*session.ToolCall,
) {
	var msg rawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// content is either a plain string (a real user prompt) or an array of
	// content blocks (most commonly a tool_result wrapper).
	var text string
	if err := json.Unmarshal(msg.Content, &text); err == nil {
		if strings.TrimSpace(text) != "" {
			sess.Messages = append(sess.Messages, session.Message{At: at, Role: "user", Text: text})
		}

		return
	}

	var blocks []rawContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}

	for i := range blocks {
		b := &blocks[i]
		if b.Type == "tool_result" && b.ToolUseID != "" {
			if tc, ok := toolCallByID[b.ToolUseID]; ok {
				tc.Output = toolResultText(b.Content)
			}
		}
	}
}

func applyAssistantEntry(
	sess *session.Session,
	raw json.RawMessage,
	at time.Time,
	toolCallByID map[string]*session.ToolCall,
) {
	var msg rawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	var blocks []rawContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}

	var text strings.Builder

	for i := range blocks {
		b := &blocks[i]

		switch b.Type {
		case "text":
			if text.Len() > 0 {
				text.WriteString("\n")
			}

			text.WriteString(b.Text)
		case "tool_use":
			tc := session.ToolCall{
				At:    at,
				Name:  b.Name,
				Input: string(b.Input),
				Files: extractFiles(b.Input),
			}
			sess.ToolCalls = append(sess.ToolCalls, tc)
			toolCallByID[b.ID] = &sess.ToolCalls[len(sess.ToolCalls)-1]
		}
	}

	if t := strings.TrimSpace(text.String()); t != "" {
		sess.Messages = append(sess.Messages, session.Message{At: at, Role: "assistant", Text: t})
	}
}

// fileInputKeys are the tool_use input fields, across Claude Code's built-in
// tools, that hold a filesystem path.
var fileInputKeys = []string{"file_path", "path", "notebook_path"}

// extractFiles looks for a filesystem path in a tool_use input. Tools like
// Bash aren't handled here: a shell command may touch many files or none,
// and guessing from its argv isn't worth the false positives.
func extractFiles(rawInput json.RawMessage) []string {
	var input map[string]any
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return nil
	}

	for _, key := range fileInputKeys {
		if v, ok := input[key].(string); ok && v != "" {
			return []string{v}
		}
	}

	return nil
}

func toolResultText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var blocks []rawContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}

	var out strings.Builder

	for i := range blocks {
		if blocks[i].Type == "text" {
			out.WriteString(blocks[i].Text)
		}
	}

	return out.String()
}

func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}

	return t
}
