package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type toolCall struct {
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type toolDef struct {
	Type     string       `json:"type"`
	Function functionSpec `json:"function"`
}

type functionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []toolDef     `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	Think    bool          `json:"think"`
}

type chatResponse struct {
	Message        chatMessage `json:"message"`
	Done           bool        `json:"done"`
	PromptEvalCount int        `json:"prompt_eval_count"`
	EvalCount       int        `json:"eval_count"`
}

type ollamaClient struct {
	baseURL string
	http    *http.Client
}

func newOllamaClient(baseURL string) *ollamaClient {
	return &ollamaClient{baseURL: baseURL, http: &http.Client{Timeout: 180 * time.Second}}
}

func (c *ollamaClient) chat(model string, messages []chatMessage, tools []toolDef) (chatResponse, time.Duration, error) {
	req := chatRequest{Model: model, Messages: messages, Tools: tools, Stream: false, Think: false}
	body, err := json.Marshal(req)
	if err != nil {
		return chatResponse{}, 0, fmt.Errorf("marshal request: %w", err)
	}

	start := time.Now()
	resp, err := c.http.Post(c.baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	elapsed := time.Since(start)
	if err != nil {
		return chatResponse{}, elapsed, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatResponse{}, elapsed, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return chatResponse{}, elapsed, fmt.Errorf("status %d: %s", resp.StatusCode, string(data))
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return chatResponse{}, elapsed, fmt.Errorf("unmarshal response: %w, body=%s", err, string(data))
	}

	return cr, elapsed, nil
}
