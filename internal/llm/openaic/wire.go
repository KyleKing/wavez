package openaic

import (
	"encoding/json"

	"github.com/kyleking/wavez/internal/llm"
)

type wireRequest struct {
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
	Model         string             `json:"model"`
	Messages      []wireMessage      `json:"messages"`
	Tools         []wireTool         `json:"tools,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Temperature   float64            `json:"temperature,omitempty"`
	Stream        bool               `json:"stream"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
}

type wireToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function wireToolCallFunction `json:"function"`
}

type wireToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func toWireRequest(model string, req llm.Request) wireRequest {
	messages := make([]wireMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, wireMessage{Role: string(llm.RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		messages = append(messages, toWireMessage(m))
	}

	var tools []wireTool
	if len(req.Tools) > 0 {
		tools = make([]wireTool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = wireTool{
				Type: "function",
				Function: wireToolFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Schema,
				},
			}
		}
	}

	return wireRequest{
		Model:         model,
		Messages:      messages,
		Tools:         tools,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		Stream:        true,
		StreamOptions: &wireStreamOptions{IncludeUsage: true},
	}
}

func toWireMessage(m llm.Message) wireMessage {
	wm := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
	if len(m.ToolCalls) == 0 {
		return wm
	}
	wm.ToolCalls = make([]wireToolCall, len(m.ToolCalls))
	for i, tc := range m.ToolCalls {
		wm.ToolCalls[i] = wireToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: wireToolCallFunction{
				Name:      tc.Name,
				Arguments: string(tc.Input),
			},
		}
	}

	return wm
}

// sseChunk is one decoded "data:" event from the chat-completions stream.
type sseChunk struct {
	Usage   *sseUsage   `json:"usage"`
	Error   *sseError   `json:"error"`
	Choices []sseChoice `json:"choices"`
}

type sseChoice struct {
	FinishReason *string  `json:"finish_reason"`
	Delta        sseDelta `json:"delta"`
}

type sseDelta struct {
	Content   string             `json:"content"`
	ToolCalls []sseToolCallDelta `json:"tool_calls"`
}

// sseToolCallDelta is one fragment of one tool call; a call's Name and
// Arguments arrive split across several deltas that share the same Index.
type sseToolCallDelta struct {
	Function sseToolCallFunction `json:"function"`
	ID       string              `json:"id"`
	Index    int                 `json:"index"`
}

type sseToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type sseUsage struct {
	PromptTokensDetails *sseTokenDetails `json:"prompt_tokens_details"`
	PromptTokens        int              `json:"prompt_tokens"`
	CompletionTokens    int              `json:"completion_tokens"`
}

type sseTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

func (u *sseUsage) toLLMUsage() *llm.Usage {
	out := &llm.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
	if u.PromptTokensDetails != nil {
		out.CacheReadTokens = u.PromptTokensDetails.CachedTokens
	}

	return out
}

type sseError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func mapFinishReason(reason string) llm.StopReason {
	switch reason {
	case "tool_calls":
		return llm.StopToolUse
	case "length":
		return llm.StopMaxTokens
	default:
		return llm.StopEndTurn
	}
}
