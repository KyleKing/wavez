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

// APIKeyEnv names the environment variable FromConfig reads the endpoint's
// optional API key from. A local model server needs none.
const APIKeyEnv = "WAVEZ_DECIDE_API_KEY" //nolint:gosec // the name of a variable, not a credential

const (
	// All a one-letter reply needs, with room for a key written out.
	maxReplyTokens = 16
	// The most of a response body that is read.
	maxResponseBytes = 1 << 20
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

// WithHTTPClient replaces http.DefaultClient.
func WithHTTPClient(c *http.Client) OpenAIOption {
	return func(o *OpenAI) { o.client = c }
}

// NewOpenAI returns a backend that posts to endpoint's /chat/completions.
// The endpoint is the base URL, such as http://localhost:11434/v1. A
// timeout above zero bounds each call in addition to the caller's context.
func NewOpenAI(endpoint, model string, timeout time.Duration, opts ...OpenAIOption) *OpenAI {
	o := &OpenAI{
		client:   http.DefaultClient,
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

// Model is the model the endpoint is asked for.
func (o *OpenAI) Model() string { return o.model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream"`
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

// Answer asks the endpoint once, at temperature zero, and maps the reply
// to one of q's keys. Any transport failure, status other than 200, or
// reply that names no option is an error.
func (o *OpenAI) Answer(ctx context.Context, q Question, state string) (Answer, error) {
	prompt, err := userPrompt(q, state)
	if err != nil {
		return Answer{}, err
	}

	body, err := json.Marshal(chatRequest{
		Model: o.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		MaxTokens: maxReplyTokens,
	})
	if err != nil {
		return Answer{}, fmt.Errorf("encoding request: %w", err)
	}

	if o.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	parsed, err := o.post(ctx, body)
	if err != nil {
		return Answer{}, err
	}

	if len(parsed.Choices) == 0 {
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

func (o *OpenAI) post(ctx context.Context, body []byte) (chatResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return chatResponse{}, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return chatResponse{}, fmt.Errorf("asking %s: %w", o.model, err)
	}

	defer resp.Body.Close() //nolint:errcheck // the body has been read or abandoned by then

	if resp.StatusCode != http.StatusOK {
		return chatResponse{}, fmt.Errorf("%w: %s", ErrStatus, resp.Status)
	}

	var parsed chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&parsed); err != nil {
		return chatResponse{}, fmt.Errorf("decoding response: %w", err)
	}

	return parsed, nil
}
