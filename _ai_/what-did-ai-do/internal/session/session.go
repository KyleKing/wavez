// Package session defines the agent-agnostic intermediate representation
// that every adapter (Claude Code, Aider, ...) parses into.
package session

import "time"

// Agent identifies which AI coding agent produced a session.
type Agent string

// Supported agents.
const (
	AgentClaudeCode Agent = "claude-code"
	AgentAider      Agent = "aider"
)

// Session is one agent coding session, normalized from an agent-specific
// transcript format into a shape the rest of the pipeline can consume.
type Session struct {
	ID          string
	Agent       Agent
	ProjectPath string
	StartedAt   time.Time
	Messages    []Message
	ToolCalls   []ToolCall
}

// Message is a single turn in the conversation.
type Message struct {
	At   time.Time
	Role string
	Text string
}

// ToolCall is a single tool invocation (shell command, file edit, etc.)
// the agent made during the session.
type ToolCall struct {
	At     time.Time
	Name   string
	Input  string
	Output string
	// Files lists paths this tool call read or wrote, when the adapter can
	// determine them (e.g. Edit/Write tool_use input); empty otherwise.
	Files []string
}
