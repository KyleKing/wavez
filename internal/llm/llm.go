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
//
// Parts carries what Content cannot say. When it is set it is the whole of
// the message's content and Content is ignored, because a provider sends one
// or the other and a message that filled both would serialize as whichever
// the provider happened to prefer.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Parts      []Part     `json:"parts,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	IsError    bool       `json:"is_error,omitempty"`
}

// PartKind names what one piece of a message's content is.
type PartKind string

// Part kinds a message may carry.
const (
	PartText  PartKind = "text"
	PartImage PartKind = "image"
)

// Part is one piece of a message's content. An image carries its bytes and
// media type rather than a URL, so the provider decides how to encode it and
// nothing outside the provider has to know that OpenAI-compatible endpoints
// want a data URL.
type Part struct {
	Kind  PartKind `json:"kind"`
	Text  string   `json:"text,omitempty"`
	Media string   `json:"media,omitempty"`
	Data  []byte   `json:"data,omitempty"`
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
	// PresencePenalty and RepeatPenalty bound repetition. Both are off in
	// llama.cpp by default (0 and 1.0), and a grammar-constrained tool
	// argument cannot stop early the way free text can, so a model that
	// starts repeating inside one has no exit. Measured over this project's
	// thread logs, 7 of 128 tool arguments over 400 bytes degenerated and
	// none of 39 prose turns did.
	PresencePenalty float64 `json:"presence_penalty,omitempty"`
	RepeatPenalty   float64 `json:"repeat_penalty,omitempty"`
}

// Usage counts tokens for one model call.
type Usage struct {
	// Timings is the serving runtime's own measurement of this call, nil for
	// a provider that reports none.
	Timings         *Timings `json:"timings,omitempty"`
	InputTokens     int      `json:"input_tokens"`
	OutputTokens    int      `json:"output_tokens"`
	CacheReadTokens int      `json:"cache_read_tokens"`
	// ReasoningBytes is how much of the turn a reasoning model spent on its
	// own working, which reaches no history and no tool. A turn that
	// produced only this said nothing the loop can act on, and without the
	// count that is indistinguishable from a model choosing to stay silent.
	ReasoningBytes int `json:"reasoning_bytes,omitempty"`
}

// Timings is how fast a serving runtime ran one call and how much of the
// prompt it reused from its cache. Token counts alone cannot answer either,
// so a runtime that reports no timings leaves decode speed and prefix-cache
// reuse without a source rather than at zero.
type Timings struct {
	// PromptTokens is how many prompt tokens the runtime evaluated, which
	// excludes CachedTokens.
	PromptTokens int `json:"prompt_tokens"`
	// CachedTokens is how many prompt tokens the runtime took from its
	// prefix cache instead of evaluating.
	CachedTokens    int     `json:"cached_tokens"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	// DecodePerSecond is generation speed, which is the local bottleneck.
	DecodePerSecond float64 `json:"decode_per_second"`
}

// PrefixHit is the share of the prompt served from the prefix cache, and is
// zero for a prompt with no tokens.
func (t Timings) PrefixHit() float64 {
	total := t.PromptTokens + t.CachedTokens
	if total == 0 {
		return 0
	}

	return float64(t.CachedTokens) / float64(total)
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
