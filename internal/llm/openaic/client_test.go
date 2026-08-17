package openaic_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/openaic"
)

var update = flag.Bool("update", false, "update golden SSE fixtures")

// The types below mirror the wire shape of one SSE "data:" event, used only
// to build test fixtures without hand-typing long JSON literals.
type evtFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type evtToolCall struct {
	Function evtFunction `json:"function"`
	ID       string      `json:"id,omitempty"`
	Index    int         `json:"index"`
}

type evtDelta struct {
	Content   string        `json:"content,omitempty"`
	ToolCalls []evtToolCall `json:"tool_calls,omitempty"`
}

type evtChoice struct {
	FinishReason *string  `json:"finish_reason"`
	Delta        evtDelta `json:"delta"`
}

type evtUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type evtError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type sseEvent struct {
	Usage   *evtUsage   `json:"usage,omitempty"`
	Error   *evtError   `json:"error,omitempty"`
	Choices []evtChoice `json:"choices,omitempty"`
}

const evtDone = "[DONE]"

func strp(s string) *string { return &s }

func event(t *testing.T, e sseEvent) string {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshaling test event: %v", err)
	}

	return string(b)
}

func sseBody(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}

	return b.String()
}

// goldenSSE returns body, writing it to testdata/<name>.golden first when
// -update is passed. The events built above are the source of truth; the
// golden file exists so a diff review sees exactly what changed, per
// AGENTS.md's golden-fixture convention.
func goldenSSE(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // name is a fixed test-case identifier, not attacker-controlled
	if err != nil {
		t.Fatalf("reading golden %s (run go test -update to create it): %v", path, err)
	}
	if string(want) != body {
		t.Fatalf("golden %s is stale; run go test -update", path)
	}

	return body
}

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, body); err != nil {
			t.Logf("writing SSE body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func newClient(t *testing.T, srv *httptest.Server) *openaic.Client {
	t.Helper()
	return openaic.New("test-provider", openaic.WithBaseURL(srv.URL), openaic.WithModel("m"))
}

func collectAll(client *openaic.Client, req llm.Request) ([]llm.Chunk, error) {
	var chunks []llm.Chunk
	for c, err := range client.Stream(context.Background(), req) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, c)
	}

	return chunks, nil
}

type streamTestCase struct {
	name string
	body string
	want []llm.Chunk
}

func streamTestCases(t *testing.T) []streamTestCase {
	t.Helper()
	return append(textStreamTestCases(t), toolCallStreamTestCases(t)...)
}

func textStreamTestCases(t *testing.T) []streamTestCase {
	t.Helper()
	return []streamTestCase{
		{
			name: "text_only_with_usage",
			body: goldenSSE(t, "text_only_with_usage", sseBody(
				event(t, sseEvent{Choices: []evtChoice{{Delta: evtDelta{Content: "Hello"}}}}),
				event(t, sseEvent{Choices: []evtChoice{{Delta: evtDelta{Content: ", world"}}}}),
				event(t, sseEvent{
					Choices: []evtChoice{{FinishReason: strp("stop")}},
					Usage:   &evtUsage{PromptTokens: 12, CompletionTokens: 4},
				}),
				evtDone,
			)),
			want: []llm.Chunk{
				{Kind: llm.ChunkText, Text: "Hello"},
				{Kind: llm.ChunkText, Text: ", world"},
				{
					Kind:       llm.ChunkDone,
					StopReason: llm.StopEndTurn,
					Usage:      &llm.Usage{InputTokens: 12, OutputTokens: 4},
				},
			},
		},
		{
			name: "max_tokens_finish_reason",
			body: goldenSSE(t, "max_tokens_finish_reason", sseBody(
				event(t, sseEvent{Choices: []evtChoice{{Delta: evtDelta{Content: "cut off"}}}}),
				event(t, sseEvent{Choices: []evtChoice{{FinishReason: strp("length")}}}),
				evtDone,
			)),
			want: []llm.Chunk{
				{Kind: llm.ChunkText, Text: "cut off"},
				{Kind: llm.ChunkDone, StopReason: llm.StopMaxTokens},
			},
		},
	}
}

func toolCallStreamTestCases(t *testing.T) []streamTestCase {
	t.Helper()

	toolCallStart := event(t, sseEvent{Choices: []evtChoice{{
		Delta: evtDelta{ToolCalls: []evtToolCall{
			{Index: 0, ID: "call_1", Function: evtFunction{Name: "str_replace"}},
		}},
	}}})
	toolCallArgs1 := event(t, sseEvent{Choices: []evtChoice{{
		Delta: evtDelta{ToolCalls: []evtToolCall{{Index: 0, Function: evtFunction{Arguments: `{"path":`}}}},
	}}})
	toolCallArgs2 := event(t, sseEvent{Choices: []evtChoice{{
		Delta: evtDelta{ToolCalls: []evtToolCall{{Index: 0, Function: evtFunction{Arguments: `"a.go",`}}}},
	}}})
	toolCallArgs3AndStop := event(t, sseEvent{
		Choices: []evtChoice{{
			Delta: evtDelta{ToolCalls: []evtToolCall{
				{Index: 0, Function: evtFunction{Arguments: `"old":"x","new":"y"}`}},
			}},
			FinishReason: strp("tool_calls"),
		}},
		Usage: &evtUsage{PromptTokens: 50, CompletionTokens: 20},
	})

	toolCallOne := event(t, sseEvent{Choices: []evtChoice{{
		Delta: evtDelta{ToolCalls: []evtToolCall{
			{Index: 0, ID: "call_1", Function: evtFunction{Name: "read_file", Arguments: `{"path":"a.go"}`}},
		}},
	}}})
	toolCallTwoAndStop := event(t, sseEvent{Choices: []evtChoice{{
		Delta: evtDelta{ToolCalls: []evtToolCall{
			{Index: 1, ID: "call_2", Function: evtFunction{Name: "read_file", Arguments: `{"path":"b.go"}`}},
		}},
		FinishReason: strp("tool_calls"),
	}}})

	return []streamTestCase{
		{
			name: "tool_call_args_split_across_three_deltas",
			body: goldenSSE(t, "tool_call_args_split_across_three_deltas", sseBody(
				toolCallStart, toolCallArgs1, toolCallArgs2, toolCallArgs3AndStop, evtDone,
			)),
			want: []llm.Chunk{
				{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{
					ID:    "call_1",
					Name:  "str_replace",
					Input: json.RawMessage(`{"path":"a.go","old":"x","new":"y"}`),
				}},
				{
					Kind:       llm.ChunkDone,
					StopReason: llm.StopToolUse,
					Usage:      &llm.Usage{InputTokens: 50, OutputTokens: 20},
				},
			},
		},
		{
			name: "two_tool_calls_in_one_response",
			body: goldenSSE(t, "two_tool_calls_in_one_response", sseBody(toolCallOne, toolCallTwoAndStop, evtDone)),
			want: []llm.Chunk{
				{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{
					ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`),
				}},
				{Kind: llm.ChunkToolCall, ToolCall: &llm.ToolCall{
					ID: "call_2", Name: "read_file", Input: json.RawMessage(`{"path":"b.go"}`),
				}},
				{Kind: llm.ChunkDone, StopReason: llm.StopToolUse},
			},
		},
	}
}

func TestClient_Stream(t *testing.T) {
	t.Parallel()

	for _, tt := range streamTestCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := sseServer(t, tt.body)
			client := newClient(t, srv)
			req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}

			got, err := collectAll(client, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chunks = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestClient_Stream_MidStreamError(t *testing.T) {
	t.Parallel()
	body := goldenSSE(t, "mid_stream_error", sseBody(
		event(t, sseEvent{Choices: []evtChoice{{Delta: evtDelta{Content: "partial"}}}}),
		event(t, sseEvent{Error: &evtError{Message: "model overloaded", Type: "server_error", Code: "overloaded"}}),
	))

	srv := sseServer(t, body)
	client := newClient(t, srv)

	got, err := collectAll(client, llm.Request{})
	if len(got) != 1 || got[0].Kind != llm.ChunkText || got[0].Text != "partial" {
		t.Fatalf("chunks before error = %+v", got)
	}

	var streamErr *openaic.StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("err = %v, want *openaic.StreamError", err)
	}
	if streamErr.Message != "model overloaded" || streamErr.Type != "server_error" {
		t.Errorf("streamErr = %+v", streamErr)
	}
}

func TestClient_Stream_NonOKStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := io.WriteString(w, "server overloaded"); err != nil {
			t.Logf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client := newClient(t, srv)
	_, err := collectAll(client, llm.Request{})

	var statusErr *openaic.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want *openaic.StatusError", err)
	}
	if statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusServiceUnavailable)
	}
	if statusErr.Provider != "test-provider" {
		t.Errorf("Provider = %q, want test-provider", statusErr.Provider)
	}
}

func TestClient_Stream_ContextCancellationAbortsPromptly(t *testing.T) {
	t.Parallel()

	partial := event(t, sseEvent{Choices: []evtChoice{{Delta: evtDelta{Content: "partial"}}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, sseBody(partial)); err != nil {
			t.Logf("writing response: %v", err)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	client := newClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	next, stop := iter.Pull2(client.Stream(ctx, llm.Request{}))
	t.Cleanup(stop)

	chunk, err, ok := next()
	if !ok || err != nil {
		t.Fatalf("first chunk: ok=%v err=%v", ok, err)
	}
	if chunk.Text != "partial" {
		t.Fatalf("chunk.Text = %q", chunk.Text)
	}

	cancel()
	_, err, _ = next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestClient_Stream_SendsResponseFormat(t *testing.T) {
	t.Parallel()

	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			t.Logf("writing SSE body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client := newClient(t, srv)
	req := llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		ResponseFormat: &llm.ResponseFormat{
			Name:   "verdict",
			Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
		},
	}

	if _, err := collectAll(client, req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var got struct {
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
				Strict bool            `json:"strict"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(<-bodies, &got); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}

	if got.ResponseFormat.Type != "json_schema" || !got.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("response_format = %+v", got.ResponseFormat)
	}
	if got.ResponseFormat.JSONSchema.Name != "verdict" {
		t.Errorf("json_schema.name = %q, want verdict", got.ResponseFormat.JSONSchema.Name)
	}
	if !strings.Contains(string(got.ResponseFormat.JSONSchema.Schema), `"ok"`) {
		t.Errorf("json_schema.schema = %s", got.ResponseFormat.JSONSchema.Schema)
	}
}

// llama.cpp reads chat_template_kwargs per request and it overrides the
// server's own --chat-template-kwargs in both directions, so an unset
// Thinking must send no key at all rather than a false one.
func TestClient_Stream_SendsThinkingAsChatTemplateKwargs(t *testing.T) {
	t.Parallel()

	on, off := true, false
	tests := []struct {
		thinking *bool
		want     *bool
		name     string
	}{
		{name: "unset sends nothing", thinking: nil, want: nil},
		{name: "off", thinking: &off, want: &off},
		{name: "on", thinking: &on, want: &on},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bodies := make(chan []byte, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading request body: %v", err)
				}
				bodies <- body
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
					t.Logf("writing SSE body: %v", err)
				}
			}))
			t.Cleanup(srv.Close)

			client := newClient(t, srv)
			req := llm.Request{
				Model:    "m",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				Thinking: tc.thinking,
			}

			if _, err := collectAll(client, req); err != nil {
				t.Fatalf("Stream: %v", err)
			}

			assertEnableThinking(t, <-bodies, tc.want)
		})
	}
}

func assertEnableThinking(t *testing.T, body []byte, want *bool) {
	t.Helper()

	var got struct {
		Kwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
		} `json:"chat_template_kwargs"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}

	if want == nil {
		if got.Kwargs != nil {
			t.Fatalf("chat_template_kwargs = %+v, want it absent", got.Kwargs)
		}

		return
	}
	if got.Kwargs == nil || got.Kwargs.EnableThinking == nil || *got.Kwargs.EnableThinking != *want {
		t.Fatalf("chat_template_kwargs = %+v, want enable_thinking %v", got.Kwargs, *want)
	}
}
