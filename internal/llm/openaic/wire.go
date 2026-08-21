package openaic

import (
	"bytes"
	"encoding/json"

	"github.com/kyleking/wavez/internal/llm"
)

type wireRequest struct {
	StreamOptions  *wireStreamOptions  `json:"stream_options,omitempty"`
	ResponseFormat *wireResponseFormat `json:"response_format,omitempty"`
	// ChatTemplateKwargs is llama.cpp's per-request hook into the chat
	// template. It overrides the server's own --chat-template-kwargs in both
	// directions, which is what makes a reasoning toggle cost a request
	// rather than a model reload.
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
	Model              string         `json:"model"`
	Messages           []wireMessage  `json:"messages"`
	Tools              []wireTool     `json:"tools,omitempty"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	Temperature        float64        `json:"temperature,omitempty"`
	Stream             bool           `json:"stream"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireResponseFormat struct {
	Type       string         `json:"type"`
	JSONSchema wireJSONSchema `json:"json_schema"`
}

type wireJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
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
		Model:          model,
		Messages:       messages,
		Tools:          tools,
		MaxTokens:      req.MaxTokens,
		Temperature:    req.Temperature,
		Stream:         true,
		StreamOptions:  &wireStreamOptions{IncludeUsage: true},
		ResponseFormat: toWireResponseFormat(req.ResponseFormat),

		ChatTemplateKwargs: toChatTemplateKwargs(req.Thinking),
	}
}

func toChatTemplateKwargs(thinking *bool) map[string]any {
	if thinking == nil {
		return nil
	}

	return map[string]any{"enable_thinking": *thinking}
}

func toWireResponseFormat(rf *llm.ResponseFormat) *wireResponseFormat {
	if rf == nil {
		return nil
	}

	return &wireResponseFormat{
		Type:       "json_schema",
		JSONSchema: wireJSONSchema{Name: rf.Name, Schema: rf.Schema, Strict: true},
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
// Timings is llama-server's extension beside the standard usage block and is
// absent from every other OpenAI-compatible provider.
type sseChunk struct {
	Usage   *sseUsage   `json:"usage"`
	Error   *sseError   `json:"error"`
	Timings *sseTimings `json:"timings"`
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

// sseTimings is llama-server's timings block. PromptN counts only the tokens
// it evaluated, so CacheN beside it is what makes prefix reuse measurable.
type sseTimings struct {
	CacheN             int     `json:"cache_n"`
	PromptN            int     `json:"prompt_n"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
}

func (t *sseTimings) toLLMTimings() *llm.Timings {
	return &llm.Timings{
		PromptTokens:    t.PromptN,
		CachedTokens:    t.CacheN,
		PromptPerSecond: t.PromptPerSecond,
		DecodePerSecond: t.PredictedPerSecond,
	}
}

type sseError struct {
	Message string     `json:"message"`
	Type    string     `json:"type"`
	Code    flexString `json:"code"`
}

// flexString decodes a field a provider sends as either a string or a
// number. The OpenAI spec types an error's code as a string and OpenRouter
// sends `"code": 429`, and failing the decode loses the error the provider
// was reporting: on a dogfood run the whole run ended as "cannot unmarshal
// number into Go struct field sseError.error.code of type string" and never
// said what upstream had objected to.
type flexString string

//nolint:unparam // json.Unmarshaler fixes the signature; every shape here decodes.
func (f *flexString) UnmarshalJSON(data []byte) error {
	text := string(bytes.TrimSpace(data))
	if text == "null" {
		*f = ""

		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = flexString(s)

		return nil
	}

	*f = flexString(text)

	return nil
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
