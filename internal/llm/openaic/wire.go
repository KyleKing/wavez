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
	// Reasoning is OpenRouter's spelling of the same toggle, Thinking is
	// Z.AI's, and Provider is OpenRouter's routing preferences. Which of
	// these the request carries is the Dialect's to say.
	Reasoning       *wireReasoning `json:"reasoning,omitempty"`
	Thinking        *wireThinking  `json:"thinking,omitempty"`
	Provider        *wireProvider  `json:"provider,omitempty"`
	Model           string         `json:"model"`
	Messages        []wireMessage  `json:"messages"`
	Tools           []wireTool     `json:"tools,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	Temperature     float64        `json:"temperature,omitempty"`
	PresencePenalty float64        `json:"presence_penalty,omitempty"`
	RepeatPenalty   float64        `json:"repeat_penalty,omitempty"`
	Stream          bool           `json:"stream"`
}

type wireReasoning struct {
	Enabled *bool `json:"enabled"`
}

// wireThinking is Z.AI's reasoning toggle, whose type is a string rather
// than a boolean. Omitting it leaves the served model reasoning, since
// "enabled" is the endpoint's own default.
type wireThinking struct {
	Type string `json:"type"`
}

const (
	thinkingEnabled  = "enabled"
	thinkingDisabled = "disabled"
)

// wireProvider is OpenRouter's per-request routing policy.
type wireProvider struct {
	DataCollection string `json:"data_collection"`
}

// denyDataCollection restricts routing to providers that do not store
// prompts. It is unconditional rather than configurable: a coding agent
// sends the contents of a private repository on every turn, and the models
// this project serves all have an endpoint that qualifies. OpenRouter
// enforces it, refusing a request with "No endpoints found matching your
// data policy" when nothing does, which is the failure worth having.
const denyDataCollection = "deny"

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

// schemaFor answers the parameter schema a dialect can actually decode. A
// dialect that does not compose gets the first branch of a top-level
// `oneOf`, which is the shape every such schema states first and the one a
// caller can always satisfy; the alternatives it drops cost extra calls
// rather than correctness. A schema that is not a composition is passed
// through, as is one whose branches do not parse, since a schema this code
// cannot read is one it must not rewrite.
func schemaFor(schema json.RawMessage, d Dialect) json.RawMessage {
	if d.composesSchemas() || len(schema) == 0 {
		return schema
	}

	var composed struct {
		OneOf []json.RawMessage `json:"oneOf"` //nolint:tagliatelle // JSON Schema's own spelling
	}
	if err := json.Unmarshal(schema, &composed); err != nil || len(composed.OneOf) == 0 {
		return schema
	}

	return composed.OneOf[0]
}

func toWireRequest(model string, req llm.Request, d Dialect) wireRequest {
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
					Parameters:  schemaFor(t.Schema, d),
				},
			}
		}
	}

	return wireRequest{
		Model:           model,
		Messages:        messages,
		Tools:           tools,
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		PresencePenalty: req.PresencePenalty,
		RepeatPenalty:   repeatPenaltyFor(req.RepeatPenalty, d),
		Stream:          true,
		StreamOptions:   &wireStreamOptions{IncludeUsage: true},
		ResponseFormat:  toWireResponseFormat(req.ResponseFormat),

		ChatTemplateKwargs: toChatTemplateKwargs(req.Thinking, d),
		Reasoning:          toWireReasoning(req.Thinking, d),
		Thinking:           toWireThinking(req.Thinking, d),
		Provider:           toWireProvider(d),
	}
}

func toWireProvider(d Dialect) *wireProvider {
	if !d.deniesDataCollection() {
		return nil
	}

	return &wireProvider{DataCollection: denyDataCollection}
}

func toWireReasoning(thinking *bool, d Dialect) *wireReasoning {
	if thinking == nil || !d.readsReasoning() {
		return nil
	}

	return &wireReasoning{Enabled: thinking}
}

func toWireThinking(thinking *bool, d Dialect) *wireThinking {
	if thinking == nil || !d.readsThinkingType() {
		return nil
	}

	if *thinking {
		return &wireThinking{Type: thinkingEnabled}
	}

	return &wireThinking{Type: thinkingDisabled}
}

func toChatTemplateKwargs(thinking *bool, d Dialect) map[string]any {
	if thinking == nil || !d.readsChatTemplateKwargs() {
		return nil
	}

	return map[string]any{"enable_thinking": *thinking}
}

func repeatPenaltyFor(penalty float64, d Dialect) float64 {
	if !d.readsRepeatPenalty() {
		return 0
	}

	return penalty
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
	Content string `json:"content"`
	// Reasoning is a reasoning model's trace, which OpenRouter streams
	// beside the content and some providers spell reasoning_content. It is
	// never history: it is the model's own working, not what it said. It is
	// counted so a turn that spent its whole budget thinking can be told
	// apart from one that chose to say nothing, which is what a hosted turn
	// against `stealth/ox-alpha` looked like until this was read at all.
	Reasoning        string             `json:"reasoning"`
	ReasoningContent string             `json:"reasoning_content"`
	ToolCalls        []sseToolCallDelta `json:"tool_calls"`
}

// reasoningText is whichever spelling this provider used.
func (d sseDelta) reasoningText() string {
	if d.Reasoning != "" {
		return d.Reasoning
	}

	return d.ReasoningContent
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
