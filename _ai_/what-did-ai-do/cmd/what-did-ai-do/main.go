// Package main implements what-did-ai-do: quiz yourself on what your AI
// coding agent actually did, with active-recall comprehension checks
// generated from real Claude Code and Aider session transcripts.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/what-did-ai-do/internal/discover"
	"github.com/kyleking/what-did-ai-do/internal/tui"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version":
			fmt.Printf("what-did-ai-do %s (commit: %s, built: %s)\n", version, commit, date)
			os.Exit(0)
		case "-h", "--help":
			printHelp()
			os.Exit(0)
		case "review":
			if err := runReview(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			return
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving working directory: %w", err)
	}

	sessions, err := discover.All(cwd)
	if err != nil {
		return fmt.Errorf("discovering sessions: %w", err)
	}

	p := tea.NewProgram(tui.New(sessions))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running tui: %w", err)
	}

	return nil
}

func printHelp() {
	fmt.Printf(`what-did-ai-do - quiz yourself on what your AI coding agent actually did

Usage:
  what-did-ai-do [options]
  what-did-ai-do review [--session ID]

Options:
  -h, --help     Show this help message
  -v, --version  Show version information

The review subcommand analyzes a session for "AI slop" using your local
claude CLI (no separate API key needed) and prints a report. Without
--session, it analyzes the most recently modified session for the current
directory's project.
`)
}
