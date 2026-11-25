package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/session"
	"github.com/kyleking/wavez/internal/statusline"
)

// statuslineConfig is what a person adds to their own Claude Code settings
// to turn the line on. It is a personal choice, so init does not write it
// into the project's shared settings.
const statuslineConfig = `{
  "statusLine": {
    "type": "command",
    "command": "wavez statusline"
  }
}
`

// statusInput is what Claude Code hands a status line command on stdin, cut
// down to what the line reads.
type statusInput struct {
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Model     struct {
		ID string `json:"id"`
	} `json:"model"`
	Workspace struct {
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
}

// runStatusline prints the status line for the session Claude Code
// describes on stdin. It never fails the harness: input it cannot read, or
// a directory that is no project, prints nothing and exits 0.
func runStatusline(_ context.Context, args []string, streams stdio) error {
	var showConfig bool

	flags := flag.NewFlagSet("wavez statusline", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&showConfig, "config", false, "print the settings that turn the status line on, and nothing else")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return &usageError{msg: "wavez statusline: " + err.Error()}
	}

	if showConfig {
		_, err := io.WriteString(streams.out, statuslineConfig)

		return wrapWrite(err)
	}

	var in statusInput
	if err := json.NewDecoder(streams.in).Decode(&in); err != nil {
		return nil //nolint:nilerr // a status line that cannot read its input shows nothing
	}

	root, ok := hook.ResolveRoot(in.Workspace.ProjectDir, in.Cwd)
	if !ok {
		return nil
	}

	line := statusline.Render(statusFor(root, in))
	if line == "" {
		return nil
	}

	_, err := fmt.Fprintln(streams.out, line)

	return wrapWrite(err)
}

// statusFor gathers what the line shows from files alone: it starts no
// process, because the harness runs it after every message.
func statusFor(root string, in statusInput) statusline.Input {
	out := statusline.Input{
		ContextPercent: in.ContextWindow.UsedPercentage,
		CostUSD:        in.Cost.TotalCostUSD,
		Model:          in.Model.ID,
		MaxRounds:      config.DefaultMaxRounds,
		Color:          os.Getenv("NO_COLOR") == "",
	}

	if cfg, err := config.Load(root); err == nil {
		out.MaxRounds = cfg.MaxRounds
	}

	if state, ok := session.NewStore(root).Read(in.SessionID); ok {
		out.State = &state
	}

	if sum, err := goal.Snapshot(root); err == nil {
		out.Goal = &sum
	}

	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns > 0 {
		out.Width = columns
	}

	return out
}

func wrapWrite(err error) error {
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	return nil
}
