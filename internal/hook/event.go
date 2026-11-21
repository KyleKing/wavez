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

// EditTools is the PostToolUse matcher: the tools that write a file whose
// path is in their input. `wavez init` installs it and the handler ignores
// any other tool.
const EditTools = "Edit|Write|MultiEdit|NotebookEdit"

// ShellTool is the PreToolUse matcher: the one tool whose command the
// handler reads before it runs.
const ShellTool = "Bash"

// ErrMalformedEvent reports stdin that is not a Claude Code hook event.
var ErrMalformedEvent = errors.New("malformed hook event")

// event is the part of a hook's stdin JSON the handler reads. Every other
// field Claude Code sends is ignored, so a newer harness does not break it.
type event struct {
	SessionID string    `json:"session_id"`
	Cwd       string    `json:"cwd"`
	Name      string    `json:"hook_event_name"`
	ToolName  string    `json:"tool_name"`
	ToolInput toolInput `json:"tool_input"`
	// Prompt is the text of a UserPromptSubmit event.
	Prompt         string `json:"prompt"`
	StopHookActive bool   `json:"stop_hook_active"`
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

func readEvent(in io.Reader) (event, error) {
	var ev event

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

	return ev, nil
}
