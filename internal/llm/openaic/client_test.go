package openaic_test

import (
	"bytes"
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

// OpenRouter sends `"code": 429` where the OpenAI spec types a string, and
// failing the decode replaced the provider's own message with a decode
// error, ending a dogfood run with nothing to act on.
func TestClient_Stream_MidStreamErrorWithNumericCode(t *testing.T) {
	t.Parallel()

	body := sseBody(`{"error":{"message":"rate limited","type":"rate_limit","code":429}}`)
	client := newClient(t, sseServer(t, body))

	_, err := collectAll(client, llm.Request{})

	var streamErr *openaic.StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("err = %v, want *openaic.StreamError", err)
	}
	if streamErr.Message != "rate limited" || streamErr.Code != "429" {
		t.Errorf("streamErr = %+v, want the provider's message and code 429", streamErr)
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
// Thinking must send no key at all rather than a false one. OpenRouter reads
// none of that and takes `reasoning`, Z.AI takes `thinking` with a string
// type, so each dialect carries its own spelling and never another's.
// OpenRouter alone carries the data-collection denial, which is what keeps a
// private repository off providers that store prompts.
func TestClient_Stream_SpellsThinkingPerDialect(t *testing.T) {
	t.Parallel()

	on, off := true, false
	enabled, disabled := "enabled", "disabled"
	tests := []struct {
		thinking     *bool
		wantKwargs   *bool
		wantReason   *bool
		wantThinking *string
		name         string
		dialect      openaic.Dialect
		wantProvider bool
	}{
		{name: "llamacpp unset sends nothing", dialect: openaic.DialectLlamaCpp},
		{name: "llamacpp off", dialect: openaic.DialectLlamaCpp, thinking: &off, wantKwargs: &off},
		{name: "llamacpp on", dialect: openaic.DialectLlamaCpp, thinking: &on, wantKwargs: &on},
		{name: "openrouter unset", dialect: openaic.DialectOpenRouter, wantProvider: true},
		{
			name: "openrouter off", dialect: openaic.DialectOpenRouter,
			thinking: &off, wantReason: &off, wantProvider: true,
		},
		{
			name: "openrouter on", dialect: openaic.DialectOpenRouter,
			thinking: &on, wantReason: &on, wantProvider: true,
		},
		{name: "zai unset sends nothing", dialect: openaic.DialectZAI},
		{
			name: "zai off", dialect: openaic.DialectZAI,
			thinking: &off, wantThinking: &disabled,
		},
		{
			name: "zai on", dialect: openaic.DialectZAI,
			thinking: &on, wantThinking: &enabled,
		},
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

			client := openaic.New("test-provider", openaic.WithBaseURL(srv.URL),
				openaic.WithModel("m"), openaic.WithDialect(tc.dialect))
			req := llm.Request{
				Model:    "m",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				Thinking: tc.thinking,
			}

			if _, err := collectAll(client, req); err != nil {
				t.Fatalf("Stream: %v", err)
			}

			body := <-bodies
			assertThinkingKey(t, body, "chat_template_kwargs", "enable_thinking", tc.wantKwargs)
			assertThinkingKey(t, body, "reasoning", "enabled", tc.wantReason)
			assertThinkingType(t, body, tc.wantThinking)
			assertDataCollectionDenied(t, body, tc.wantProvider)
		})
	}
}

// A coding agent sends a private repository's contents on every turn, so
// the denial is unconditional on the dialect that honors it rather than a
// setting somebody has to find.
func assertDataCollectionDenied(t *testing.T, body []byte, want bool) {
	t.Helper()

	var got struct {
		Provider *struct {
			DataCollection string `json:"data_collection"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}

	if !want {
		if got.Provider != nil {
			t.Fatalf("provider = %+v, want it absent", got.Provider)
		}

		return
	}

	if got.Provider == nil || got.Provider.DataCollection != "deny" {
		t.Fatalf("provider = %+v, want data_collection deny", got.Provider)
	}
}

// assertThinkingKey reads one provider's spelling of the reasoning toggle
// out of the request body, where outer names the object and inner the
// boolean inside it.
func assertThinkingKey(t *testing.T, body []byte, outer, inner string, want *bool) {
	t.Helper()

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}

	raw, ok := fields[outer]
	if want == nil {
		if ok {
			t.Fatalf("%s = %s, want it absent", outer, raw)
		}

		return
	}

	if !ok {
		t.Fatalf("%s is absent, want %s %v", outer, inner, *want)
	}

	var obj map[string]*bool
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decoding %s: %v", outer, err)
	}

	if obj[inner] == nil || *obj[inner] != *want {
		t.Fatalf("%s.%s = %v, want %v", outer, inner, obj[inner], *want)
	}
}

// Z.AI types its toggle as a string, so a boolean sent there reads as a
// decode error on the provider rather than as reasoning off.
func assertThinkingType(t *testing.T, body []byte, want *string) {
	t.Helper()

	var got struct {
		Thinking *struct {
			Type string `json:"type"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}

	if want == nil {
		if got.Thinking != nil {
			t.Fatalf("thinking = %+v, want it absent", got.Thinking)
		}

		return
	}

	if got.Thinking == nil || got.Thinking.Type != *want {
		t.Fatalf("thinking = %+v, want type %q", got.Thinking, *want)
	}
}

// llamaServerFinalChunk is llama-server's own last stream event, captured
// from a real qwen3:8b turn. Its timings block is the only source for decode
// speed and prefix-cache reuse, and no other OpenAI-compatible provider sends
// one.
const llamaServerFinalChunk = `data: {"choices":[],"object":"chat.completion.chunk",` +
	`"usage":{"completion_tokens":3,"prompt_tokens":18,"total_tokens":21,` +
	`"prompt_tokens_details":{"cached_tokens":17}},` +
	`"timings":{"cache_n":17,"prompt_n":1,"prompt_ms":44.33,"prompt_per_second":22.55,` +
	`"predicted_n":3,"predicted_ms":68.04,"predicted_per_second":29.39}}` + "\n\n" +
	"data: [DONE]\n\n"

func TestClient_Stream_ParsesLlamaServerTimings(t *testing.T) {
	t.Parallel()

	client := newClient(t, sseServer(t, llamaServerFinalChunk))

	got, err := collectAll(client, llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	last := got[len(got)-1]
	if last.Kind != llm.ChunkDone || last.Usage == nil || last.Usage.Timings == nil {
		t.Fatalf("final chunk = %+v, want a done chunk carrying timings", last)
	}

	want := llm.Timings{PromptTokens: 1, CachedTokens: 17, PromptPerSecond: 22.55, DecodePerSecond: 29.39}
	if *last.Usage.Timings != want {
		t.Errorf("timings = %+v, want %+v", *last.Usage.Timings, want)
	}

	const wantHit = 17.0 / 18.0
	if got := last.Usage.Timings.PrefixHit(); got != wantHit {
		t.Errorf("PrefixHit() = %v, want %v", got, wantHit)
	}
}

// The fast tier's repetition bounds have to reach the server or they are a
// constant nobody applies. Every penalty is disabled by default in
// llama.cpp, so an unsent field is indistinguishable from no bound at all.
func TestRequestCarriesTheRepetitionBounds(t *testing.T) {
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

	req := llm.Request{
		Model:           "m",
		Messages:        []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		PresencePenalty: 1.5,
	}
	if _, err := collectAll(newClient(t, srv), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(<-bodies, &got); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}

	if got["presence_penalty"] != 1.5 {
		t.Errorf("presence_penalty = %v, want 1.5", got["presence_penalty"])
	}

	if _, ok := got["repeat_penalty"]; ok {
		t.Errorf("repeat_penalty = %v, want it omitted when unset", got["repeat_penalty"])
	}
}

// A tool schema stating alternative input shapes as a top-level `oneOf` is
// what llama-server compiles its grammar from, and it is also what GLM-5.3
// answers with `{}`, so the shape a request carries has to follow its
// dialect the way the reasoning toggle does.
func TestClient_Stream_FlattensComposedSchemaOnlyWhereItIsNotRead(t *testing.T) {
	t.Parallel()

	composed := json.RawMessage(`{"oneOf":[` +
		`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]},` +
		`{"type":"object","properties":{"edits":{"type":"array"}},"required":["edits"]}]}`)
	plain := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)

	tests := []struct {
		name    string
		dialect openaic.Dialect
		schema  json.RawMessage
		wantOne bool
	}{
		{name: "zai gets the first branch", schema: composed, dialect: openaic.DialectZAI},
		{name: "openrouter keeps the composition", schema: composed, dialect: openaic.DialectOpenRouter, wantOne: true},
		{name: "llamacpp keeps the composition", schema: composed, dialect: openaic.DialectLlamaCpp, wantOne: true},
		{name: "a plain schema is untouched on zai", schema: plain, dialect: openaic.DialectZAI},
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

			client := openaic.New("test-provider", openaic.WithBaseURL(srv.URL),
				openaic.WithModel("m"), openaic.WithDialect(tc.dialect))
			req := llm.Request{
				Model:    "m",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				Tools:    []llm.ToolSpec{{Name: "str_replace", Schema: tc.schema}},
			}

			if _, err := collectAll(client, req); err != nil {
				t.Fatalf("Stream: %v", err)
			}

			assertSchemaShape(t, sentToolSchema(t, <-bodies), tc.schema, tc.dialect, tc.wantOne)
		})
	}
}

// assertSchemaShape checks that a dialect was sent the composition it reads
// and, where it was not, that the branch it got kept its own requirements
// and none of the branch it lost.
func assertSchemaShape(t *testing.T, sent, orig json.RawMessage, d openaic.Dialect, wantOne bool) {
	t.Helper()

	if got := bytes.Contains(sent, []byte(`"oneOf"`)); got != wantOne {
		t.Fatalf("schema sent to %s composed = %v, want %v: %s", d, got, wantOne, sent)
	}

	if wantOne || !bytes.Contains(orig, []byte(`"oneOf"`)) {
		return
	}

	if !bytes.Contains(sent, []byte(`"required"`)) {
		t.Errorf("the flattened branch dropped its own required fields: %s", sent)
	}
	if bytes.Contains(sent, []byte(`"edits"`)) {
		t.Errorf("the dropped branch leaked into the flattened one: %s", sent)
	}
}

// sentToolSchema digs the one advertised tool's parameter schema back out of
// a recorded request body.
func sentToolSchema(t *testing.T, body []byte) json.RawMessage {
	t.Helper()

	var sent struct {
		Tools []struct {
			Function struct {
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	if len(sent.Tools) != 1 {
		t.Fatalf("advertised %d tools, want 1", len(sent.Tools))
	}

	return sent.Tools[0].Function.Parameters
}

// An image reaches a model as a content array, and a message without parts
// must serialize exactly as it always has: a provider's prompt-cache prefix
// is the bytes, so a text message that started sending `"content": []` would
// invalidate every cached prefix at once.
func TestClient_Stream_SendsAnImageAsAContentArray(t *testing.T) {
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
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "plain"},
			{Role: llm.RoleUser, Parts: []llm.Part{
				{Kind: llm.PartText, Text: "what is this"},
				{Kind: llm.PartImage, Media: "image/png", Data: []byte{1, 2, 3}},
			}},
		},
	}

	if _, err := collectAll(client, req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var got struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(<-bodies, &got); err != nil {
		t.Fatalf("decoding the request: %v", err)
	}

	last := len(got.Messages) - 1
	if want := `{"role":"user","content":"plain"}`; string(got.Messages[last-1]) != want {
		t.Errorf("text message = %s, want it unchanged at %s", got.Messages[last-1], want)
	}

	want := `{"role":"user","content":[{"type":"text","text":"what is this"},` +
		`{"image_url":{"url":"data:image/png;base64,AQID"},"type":"image_url"}]}`
	if string(got.Messages[last]) != want {
		t.Errorf("image message = %s, want %s", got.Messages[last], want)
	}
}
