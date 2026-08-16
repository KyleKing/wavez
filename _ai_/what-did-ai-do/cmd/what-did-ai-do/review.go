package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/adversarial"
	"github.com/kyleking/what-did-ai-do/internal/discover"
	"github.com/kyleking/what-did-ai-do/internal/findingsstore"
	"github.com/kyleking/what-did-ai-do/internal/llm"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

var (
	errSessionNotFound   = errors.New("no session found with that ID")
	errNoSessionsForRepo = errors.New("no sessions found for this project; pass --session to analyze one elsewhere")
)

func runReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	sessionID := fs.String("session", "", "session ID to analyze (default: most recent session for this project)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing review flags: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving working directory: %w", err)
	}

	target, err := selectSession(cwd, *sessionID)
	if err != nil {
		return err
	}

	report, err := adversarial.New(llm.NewClient()).Analyze(context.Background(), target)
	if err != nil {
		return fmt.Errorf("analyzing session %s: %w", target.ID, err)
	}

	stored := findingsstore.FromAnalyzer(report, time.Now().UTC())
	if err := findingsstore.Save(stored); err != nil {
		return fmt.Errorf("saving report: %w", err)
	}

	fmt.Print(findingsstore.Render(stored))

	return nil
}

func selectSession(cwd, sessionID string) (*session.Session, error) {
	sessions, err := discover.All(cwd)
	if err != nil {
		return nil, fmt.Errorf("discovering sessions: %w", err)
	}

	if sessionID != "" {
		for i := range sessions {
			if sessions[i].ID == sessionID {
				return &sessions[i], nil
			}
		}

		return nil, fmt.Errorf("%w: %q", errSessionNotFound, sessionID)
	}

	for i := range sessions {
		if sessions[i].ProjectPath == cwd {
			return &sessions[i], nil
		}
	}

	return nil, fmt.Errorf("%w: %q", errNoSessionsForRepo, cwd)
}
