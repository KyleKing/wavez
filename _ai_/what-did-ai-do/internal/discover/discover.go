// Package discover finds and parses local agent sessions across every
// supported adapter, producing the combined list the TUI displays.
package discover

import (
	"fmt"
	"sort"

	"github.com/kyleking/what-did-ai-do/internal/adapter/aider"
	"github.com/kyleking/what-did-ai-do/internal/adapter/claudecode"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// All discovers every Claude Code session under ~/.claude/projects (across
// all projects) and every Aider session under cwd, most recent first.
// A file that fails to parse is skipped rather than failing the whole scan,
// since one corrupt transcript shouldn't hide every other session.
func All(cwd string) ([]session.Session, error) {
	var sessions []session.Session

	claudeSessions, err := claudeCodeSessions()
	if err != nil {
		return nil, err
	}

	sessions = append(sessions, claudeSessions...)

	aiderSessions, err := aiderSessions(cwd)
	if err != nil {
		return nil, err
	}

	sessions = append(sessions, aiderSessions...)

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	return sessions, nil
}

func claudeCodeSessions() ([]session.Session, error) {
	projectsDir, err := claudecode.DefaultProjectsDir()
	if err != nil {
		return nil, fmt.Errorf("resolving claude code projects dir: %w", err)
	}

	paths, err := claudecode.Discover(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("discovering claude code sessions: %w", err)
	}

	sessions := make([]session.Session, 0, len(paths))

	for _, path := range paths {
		s, err := claudecode.Parse(path)
		if err != nil {
			continue
		}

		sessions = append(sessions, s)
	}

	return sessions, nil
}

func aiderSessions(cwd string) ([]session.Session, error) {
	paths, err := aider.Discover(cwd)
	if err != nil {
		return nil, fmt.Errorf("discovering aider sessions: %w", err)
	}

	var sessions []session.Session

	for _, path := range paths {
		parsed, err := aider.Parse(path)
		if err != nil {
			continue
		}

		sessions = append(sessions, parsed...)
	}

	return sessions, nil
}
