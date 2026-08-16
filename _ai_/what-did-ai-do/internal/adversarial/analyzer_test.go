package adversarial_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/adversarial"
	"github.com/kyleking/what-did-ai-do/internal/llm"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

type fakeClient struct {
	err       error
	judgments []llm.Judgment
	calls     int
}

func (f *fakeClient) Judge(_ context.Context, _, _, _ string) ([]llm.Judgment, error) {
	f.calls++
	return f.judgments, f.err
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}

func liveSession(t *testing.T, projectPath string) *session.Session {
	t.Helper()

	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	return &session.Session{
		ID:          "sess-1",
		Agent:       session.AgentClaudeCode,
		ProjectPath: projectPath,
		ToolCalls: []session.ToolCall{
			{
				At:    start,
				Name:  "Edit",
				Files: []string{"foo.go"},
				Input: `{"old_string":"a","new_string":"package foo\n\nfunc F() {}\n"}`,
			},
			{
				At:    start.Add(time.Second),
				Name:  "Bash",
				Input: `{"command":"rm -rf node_modules && npm install --force"}`,
			},
		},
	}
}

func TestAnalyze_FlagsHighConfidenceQuestionableFindings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "foo.go", "package foo\n\nfunc F() {}\n")

	client := &fakeClient{judgments: []llm.Judgment{
		{DecisionID: "sess-1-000", Assessment: "sound", Category: "none", Confidence: 0.9},
		{
			DecisionID: "sess-1-001", Assessment: "slop", Category: "own-choice",
			Confidence: 0.85, Concern: "destructive, undiagnosed",
		},
	}}

	report, err := adversarial.New(client).Analyze(context.Background(), liveSession(t, dir))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if report.Analyzed != 2 {
		t.Errorf("Analyzed = %d, want 2", report.Analyzed)
	}

	if len(report.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(report.Findings))
	}

	if report.Findings[0].Judgment.DecisionID != "sess-1-001" {
		t.Errorf(
			"Findings[0].Judgment.DecisionID = %q, want %q",
			report.Findings[0].Judgment.DecisionID,
			"sess-1-001",
		)
	}
}

func TestAnalyze_LowConfidenceFindingIsFiltered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	client := &fakeClient{judgments: []llm.Judgment{
		{
			DecisionID: "sess-1-000",
			Assessment: "questionable",
			Category:   "unconsidered-alternative",
			Confidence: 0.5,
		},
		{
			DecisionID: "sess-1-001",
			Assessment: "questionable",
			Category:   "own-choice",
			Confidence: 0.5,
		},
	}}

	report, err := adversarial.New(client).Analyze(context.Background(), liveSession(t, dir))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if len(report.Findings) != 0 {
		t.Errorf("len(Findings) = %d, want 0 (below confidence threshold)", len(report.Findings))
	}
}

func TestAnalyze_SupersededFileSkipsLLMCall(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// foo.go on disk no longer contains the decision's new_string.
	writeFile(t, dir, "foo.go", "package foo\n\n// rewritten entirely\n")

	s := &session.Session{
		ID:          "sess-2",
		Agent:       session.AgentClaudeCode,
		ProjectPath: dir,
		ToolCalls: []session.ToolCall{
			{
				At:    time.Now().UTC(),
				Name:  "Edit",
				Files: []string{"foo.go"},
				Input: `{"old_string":"a","new_string":"package foo\n\nfunc F() {}\n"}`,
			},
		},
	}

	client := &fakeClient{}

	report, err := adversarial.New(client).Analyze(context.Background(), s)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if client.calls != 0 {
		t.Errorf("Judge() called %d times, want 0 (only candidate is superseded)", client.calls)
	}

	if report.Analyzed != 0 {
		t.Errorf("Analyzed = %d, want 0", report.Analyzed)
	}
}

func TestAnalyze_NoCandidatesSkipsLLMCall(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}

	report, err := adversarial.New(client).
		Analyze(context.Background(), &session.Session{ID: "sess-empty"})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if client.calls != 0 {
		t.Errorf("Judge() called %d times, want 0", client.calls)
	}

	if report.SessionID != "sess-empty" {
		t.Errorf("SessionID = %q, want %q", report.SessionID, "sess-empty")
	}
}

func TestAnalyze_ClientErrorPropagates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	client := &fakeClient{err: exec.ErrNotFound}

	_, err := adversarial.New(client).Analyze(context.Background(), liveSession(t, dir))
	if err == nil {
		t.Error("Analyze() error = nil, want the client's error to propagate")
	}
}
