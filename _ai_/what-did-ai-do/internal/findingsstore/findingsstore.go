// Package findingsstore persists adversarial analysis reports to disk so
// the (paid, opt-in) `review` command's output can be read back cheaply by
// the TUI without re-running the LLM.
package findingsstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/adversarial"
)

// dirPerm and filePerm are the filesystem permissions for the findings
// cache directory and the report files within it.
const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// Finding is a serializable projection of adversarial.Finding: enough to
// render a report without depending on the live gitstate.FileState/
// llm.Judgment shapes at read time.
type Finding struct {
	DecisionID string  `json:"decision_id"`
	Summary    string  `json:"summary"`
	Assessment string  `json:"assessment"`
	Category   string  `json:"category"`
	Concern    string  `json:"concern"`
	Suggestion string  `json:"suggestion,omitempty"`
	Confidence float64 `json:"confidence"`
}

// Report is the persisted outcome of one `review` run over one session.
type Report struct {
	AnalyzedAt time.Time `json:"analyzed_at"`
	SessionID  string    `json:"session_id"`
	Findings   []Finding `json:"findings"`
	Analyzed   int       `json:"analyzed"`
}

// FromAnalyzer converts an adversarial.Report into its persisted form.
func FromAnalyzer(r adversarial.Report, analyzedAt time.Time) Report {
	findings := make([]Finding, 0, len(r.Findings))

	for i := range r.Findings {
		f := &r.Findings[i]
		findings = append(findings, Finding{
			DecisionID: f.Judgment.DecisionID,
			Summary:    f.Candidate.Decision.Summary,
			Assessment: f.Judgment.Assessment,
			Category:   f.Judgment.Category,
			Confidence: f.Judgment.Confidence,
			Concern:    f.Judgment.Concern,
			Suggestion: f.Judgment.Suggestion,
		})
	}

	return Report{SessionID: r.SessionID, Analyzed: r.Analyzed, Findings: findings, AnalyzedAt: analyzedAt}
}

// dir returns the directory findings are stored under, creating it if
// needed.
func dir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving cache directory: %w", err)
	}

	path := filepath.Join(cacheDir, "what-did-ai-do", "findings")
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return "", fmt.Errorf("creating findings directory: %w", err)
	}

	return path, nil
}

func path(sessionID string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(d, sessionID+".json"), nil
}

// Save writes r to disk, keyed by its SessionID.
func Save(r Report) error {
	p, err := path(r.SessionID)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding report for session %s: %w", r.SessionID, err)
	}

	if err := os.WriteFile(p, data, filePerm); err != nil {
		return fmt.Errorf("writing report for session %s: %w", r.SessionID, err)
	}

	return nil
}

// Render formats r as a plain-text report, shared by the `review` CLI
// command and the TUI's findings screen so they never drift.
func Render(r Report) string {
	if len(r.Findings) == 0 {
		return fmt.Sprintf("Analyzed %d decision(s) in session %s: no findings.\n", r.Analyzed, r.SessionID)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Analyzed %d decision(s) in session %s: %d finding(s)\n\n",
		r.Analyzed, r.SessionID, len(r.Findings))

	for i := range r.Findings {
		f := &r.Findings[i]

		fmt.Fprintf(&b, "[%s/%s, confidence %.2f] %s\n  %s\n",
			f.Assessment, f.Category, f.Confidence, f.Summary, f.Concern)

		if f.Suggestion != "" {
			fmt.Fprintf(&b, "  suggestion: %s\n", f.Suggestion)
		}

		b.WriteString("\n")
	}

	return b.String()
}

// Load reads a previously saved report for sessionID. The bool return is
// false (with a nil error) when no report has been saved for that session
// yet.
func Load(sessionID string) (Report, bool, error) {
	p, err := path(sessionID)
	if err != nil {
		return Report{}, false, err
	}

	data, err := os.ReadFile(p) //nolint:gosec // path is built from a session ID we generated, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return Report{}, false, nil
		}

		return Report{}, false, fmt.Errorf("reading report for session %s: %w", sessionID, err)
	}

	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, false, fmt.Errorf("decoding report for session %s: %w", sessionID, err)
	}

	return report, true, nil
}
