// Package openaic implements llm.Provider against any OpenAI-compatible
// chat-completions endpoint over SSE streaming. It serves both llama-server
// and OpenRouter, since both expose the same /chat/completions wire format.
package openaic

import (
	"net/http"
	"strings"
)

// Client streams chat completions from one OpenAI-compatible endpoint.
type Client struct {
	httpClient *http.Client
	headers    map[string]string
	name       string
	baseURL    string
	apiKey     string
	model      string
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL sets the endpoint root; Stream posts to baseURL + "/chat/completions".
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(url, "/") }
}

// WithAPIKey sets the bearer token sent as an Authorization header.
func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

// WithModel sets the model name sent in each request body.
func WithModel(model string) Option {
	return func(c *Client) { c.model = model }
}

// WithHTTPClient overrides the client used to send requests, for timeouts or transport tuning.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithHeader sets one extra header sent with every request, such as
// OpenRouter's attribution headers.
func WithHeader(key, value string) Option {
	return func(c *Client) { c.headers[key] = value }
}

// New builds a Client identified by name, the provider label Name returns and
// every error from this Client carries.
func New(name string, opts ...Option) *Client {
	c := &Client{
		name:       name,
		httpClient: http.DefaultClient,
		headers:    map[string]string{},
	}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Name implements llm.Provider.
func (c *Client) Name() string { return c.name }
