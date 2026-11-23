package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// The hook_event_name values wavez acts on.
const (
	eventPostToolUse  = "PostToolUse"
	eventStop         = "Stop"
	eventSessionStart = "SessionStart"
	// UserPromptSubmit carries the prompt a person just sent, and
	// PreToolUse asks whether a tool call may run.
	eventUserPromptSubmit = "UserPromptSubmit"
	eventPreToolUse       = "PreToolUse"
)

// sourceResume is the SessionStart source of a session picked up again,
// whose context already holds whatever its first start was told.
const sourceResume = "resume"

// EditTools is the PostToolUse matcher: the tools that write a file whose
// path is in their input. `wavez init` installs it and the handler ignores
// any other tool.
const EditTools = "Edit|Write|MultiEdit|NotebookEdit"

// ShellTool is the PreToolUse matcher: the one tool whose command the
// handler reads before it runs.
const ShellTool = "Bash"

// ErrMalformedEvent reports stdin that is not a Claude Code hook event.
var ErrMalformedEvent = errors.New("malformed hook event")

// Event is the part of a hook's stdin JSON the handler reads. Every other
// field Claude Code sends is ignored, so a newer harness does not break it.
type Event struct {
	SessionID string `json:"session_id"`
	// Cwd is where the agent is now, which is not where the session
	// started once it has changed directory.
	Cwd       string    `json:"cwd"`
	Name      string    `json:"hook_event_name"`
	ToolName  string    `json:"tool_name"`
	ToolInput toolInput `json:"tool_input"`
	// Prompt is the text of a UserPromptSubmit event.
	Prompt string `json:"prompt"`
	// Source says why a SessionStart fired: startup, resume, clear, or
	// compact.
	Source string `json:"source"`
	// StopHookActive is true when this stop is itself a continuation that a
	// stop hook caused.
	StopHookActive bool `json:"stop_hook_active"`
}

// toolInput holds the path an edit tool wrote, or the command the shell
// tool is about to run. NotebookEdit names the path notebook_path where the
// others say file_path.
type toolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
}

func (t toolInput) path() string {
	if t.FilePath != "" {
		return t.FilePath
	}

	return t.NotebookPath
}

// ReadEvent reads one hook event from in.
func ReadEvent(in io.Reader) (Event, error) {
	var ev Event

	raw, err := io.ReadAll(in)
	if err != nil {
		return ev, fmt.Errorf("reading hook event: %w", err)
	}

	if err := json.Unmarshal(raw, &ev); err != nil {
		return ev, fmt.Errorf("%w: %w", ErrMalformedEvent, err)
	}

	if ev.Name == "" {
		return ev, fmt.Errorf("%w: no hook_event_name", ErrMalformedEvent)
	}

	if ev.Cwd == "" {
		return ev, fmt.Errorf("%w: no cwd", ErrMalformedEvent)
	}

	return ev, nil
}
