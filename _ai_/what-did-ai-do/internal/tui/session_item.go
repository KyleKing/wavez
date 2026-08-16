package tui

import (
	"fmt"

	"github.com/kyleking/what-did-ai-do/internal/findingsstore"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// sessionItem adapts a parsed session.Session to bubbles/list's DefaultItem
// so it can be shown in the session-picker screen.
type sessionItem struct {
	session session.Session
}

func (i *sessionItem) Title() string {
	label := i.session.ID
	if label == "" {
		label = "(untitled session)"
	}

	return fmt.Sprintf("[%s] %s", i.session.Agent, label)
}

func (i *sessionItem) Description() string {
	desc := fmt.Sprintf(
		"%s — %s — %d tool calls",
		i.session.ProjectPath,
		i.session.StartedAt.Format("2006-01-02 15:04"),
		len(i.session.ToolCalls),
	)

	// Findings are produced by the separate, opt-in `review` CLI command
	// (real LLM cost), not generated here; this only reads what's cached.
	if report, found, err := findingsstore.Load(i.session.ID); err == nil && found && len(report.Findings) > 0 {
		desc += fmt.Sprintf(" — ⚠ %d finding(s)", len(report.Findings))
	}

	return desc
}

func (i *sessionItem) FilterValue() string {
	return i.session.ProjectPath + " " + i.session.ID
}
