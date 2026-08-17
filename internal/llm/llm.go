// Package llm defines the provider-agnostic model interface the agent loop drives.
package llm

import (
	"context"
	"encoding/json"
	"iter"
)

// Role identifies who produced a Message.
type Role string

// Roles a Message may carry.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry in a thread's history. History is append-only so a
// provider's prompt-cache prefix stays valid across turns.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	IsError    bool       `json:"is_error,omitempty"`
}

// ToolCall is a model request to run one tool.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolSpec advertises a tool to the model. Schema is a JSON Schema object.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// ResponseFormat constrains a model's output to a JSON Schema, which the
// llama-server runtime enforces as a grammar so a served local model cannot
// answer off-schema. A provider that does not support it ignores it, so a
// caller must still parse defensively.
type ResponseFormat struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// Request is one model call. System and Tools are held stable across a thread
// so they form a cacheable prefix.
type Request struct {
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	// Thinking turns a hybrid model's reasoning trace on or off for this one
	// call. Nil leaves the served model's own default alone, which is the
	// only way to say "do not care": the flag is meaningful in both states.
	// Measured on qwen3:8b, replying "OK" costs 79 completion tokens with it
	// on and 2 with it off.
	Thinking    *bool      `json:"thinking,omitempty"`
	Model       string     `json:"model"`
	System      string     `json:"system,omitempty"`
	Messages    []Message  `json:"messages"`
	Tools       []ToolSpec `json:"tools,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Temperature float64    `json:"temperature,omitempty"`
}

// Usage counts tokens for one model call.
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
}

// StopReason explains why the model stopped generating.
type StopReason string

// Reasons a model stops generating.
const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
)

// ChunkKind discriminates the Chunk union.
type ChunkKind string

// Kinds a streamed Chunk may take.
const (
	ChunkText     ChunkKind = "text"
	ChunkToolCall ChunkKind = "tool_call"
	ChunkDone     ChunkKind = "done"
)

// Chunk is one streamed unit. A ChunkDone chunk carries Usage and StopReason
// and is always the last chunk of a successful stream.
type Chunk struct {
	Kind       ChunkKind
	Text       string
	ToolCall   *ToolCall
	Usage      *Usage
	StopReason StopReason
}

// Provider is one model backend. Stream yields chunks in order; iteration stops
// at the first non-nil error.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error]
}
