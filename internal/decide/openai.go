package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// All a one-letter reply needs, with room for a key written out. It
	// leaves no room to think, which is why thinking is switched off.
	maxReplyTokens = 16
	// The most of a response body that is read. A one-letter completion
	// with its usage block is a few hundred bytes.
	maxResponseBytes = 64 << 10
	// The reasoning_effort value that llama-server and Ollama both read
	// as thinking off.
	effortNone = "none"
)

// Errors the OpenAI backend returns.
var (
	// ErrStatus is returned when the endpoint answers with anything but
	// 200.
	ErrStatus = errors.New("unexpected status")
	// ErrNoChoices is returned when a response carries no completion.
	ErrNoChoices = errors.New("response has no choices")
)

// OpenAI is a Backend over an OpenAI-compatible chat completions endpoint,
// which is what ollama, llama.cpp, and LM Studio serve.
//
// The model has to answer in a handful of tokens, so it should be a
// non-thinking model or have thinking off. Every request asks for that in
// the two ways local servers read: reasoning_effort "none" (llama-server,
// Ollama) and chat_template_kwargs {"enable_thinking": false} (llama-server,
// vLLM). A server that refuses the request over those fields with a 400 or
// a 422 is asked once more without them.
type OpenAI struct {
	client   *http.Client
	endpoint string
	model    string
	apiKey   string
	timeout  time.Duration
}

// OpenAIOption configures an OpenAI backend.
type OpenAIOption func(*OpenAI)

// WithAPIKey sends key as a bearer token. An empty key sends no header.
func WithAPIKey(key string) OpenAIOption {
	return func(o *OpenAI) { o.apiKey = key }
}

// NewOpenAI returns a backend that posts to endpoint's /chat/completions.
// The endpoint is the base URL, such as http://localhost:11434/v1. A
// timeout above zero bounds each call in addition to the caller's context.
// A redirect is not followed, so the prompt and the key go only to the host
// the endpoint names.
func NewOpenAI(endpoint, model string, timeout time.Duration, opts ...OpenAIOption) *OpenAI {
	o := &OpenAI{
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		endpoint: strings.TrimRight(endpoint, "/"),
		model:    model,
		timeout:  timeout,
	}
	for _, opt := range opts {
		opt(o)
	}

	return o
}

// Name is the value Decision.Backend takes for an answer from this backend.
func (*OpenAI) Name() string { return "openai" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	ChatTemplateKwargs map[string]bool `json:"chat_template_kwargs,omitempty"`
	Model              string          `json:"model"`
	ReasoningEffort    string          `json:"reasoning_effort,omitempty"`
	Messages           []chatMessage   `json:"messages"`
	Temperature        float64         `json:"temperature"`
	MaxTokens          int             `json:"max_tokens"`
	Stream             bool            `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Answer asks the endpoint at temperature zero and maps the reply to one
// of q's keys. Any transport failure, status other than 200, or reply that
// is not one option is an error.
func (o *OpenAI) Answer(ctx context.Context, q Question, state string) (Answer, error) {
	prompt, err := userPrompt(q, state)
	if err != nil {
		return Answer{}, err
	}

	if o.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	request := chatRequest{
		Model: o.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		MaxTokens:          maxReplyTokens,
		ReasoningEffort:    effortNone,
		ChatTemplateKwargs: map[string]bool{"enable_thinking": false},
	}

	parsed, status, err := o.post(ctx, request)
	if err == nil && (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity) {
		request.ReasoningEffort, request.ChatTemplateKwargs = "", nil
		parsed, status, err = o.post(ctx, request)
	}

	switch {
	case err != nil:
		return Answer{}, err
	case status != http.StatusOK:
		return Answer{}, fmt.Errorf("%w: %d %s", ErrStatus, status, http.StatusText(status))
	case len(parsed.Choices) == 0:
		return Answer{}, ErrNoChoices
	}

	key, err := parseReply(q, parsed.Choices[0].Message.Content)
	if err != nil {
		return Answer{}, err
	}

	return Answer{
		Key:          key,
		Model:        o.model,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}, nil
}

// post sends one request and returns the status with the body decoded
// when it is 200. A body longer than maxResponseBytes fails to decode.
func (o *OpenAI) post(ctx context.Context, request chatRequest) (chatResponse, int, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return chatResponse{}, 0, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return chatResponse{}, 0, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return chatResponse{}, 0, fmt.Errorf("asking %s: %w", o.model, err)
	}

	defer resp.Body.Close() //nolint:errcheck // the body has been read or abandoned by then

	if resp.StatusCode != http.StatusOK {
		return chatResponse{}, resp.StatusCode, nil
	}

	var parsed chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&parsed); err != nil {
		return chatResponse{}, resp.StatusCode, fmt.Errorf("decoding response: %w", err)
	}

	return parsed, resp.StatusCode, nil
}
