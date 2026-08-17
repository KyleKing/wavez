package openaic_test

import (
	"context"
	"os"
	"testing"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/openaic"
)

// TestClient_Stream_LlamaServer exercises a real llama-server. It is skipped
// unless WAVEZ_LLAMA_SERVER_URL names a running server's OpenAI-compatible
// base URL (e.g. http://127.0.0.1:8090/v1), so it never runs in CI.
func TestClient_Stream_LlamaServer(t *testing.T) {
	t.Parallel()

	baseURL := os.Getenv("WAVEZ_LLAMA_SERVER_URL")
	if baseURL == "" {
		t.Skip("WAVEZ_LLAMA_SERVER_URL not set; skipping live llama-server integration test")
	}

	client := openaic.New("llama-server", openaic.WithBaseURL(baseURL), openaic.WithModel("qwen3-8b"))

	req := llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Reply with exactly the word: pong"}},
	}

	var text string
	var stopReason llm.StopReason
	for chunk, err := range client.Stream(context.Background(), req) {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		switch chunk.Kind {
		case llm.ChunkText:
			text += chunk.Text
		case llm.ChunkDone:
			stopReason = chunk.StopReason
		case llm.ChunkToolCall:
		}
	}

	if text == "" {
		t.Error("got no text from llama-server")
	}
	if stopReason == "" {
		t.Error("got no stop reason from llama-server")
	}
}

// TestClient_Stream_ThinkingIsPerRequest proves the reasoning toggle costs a
// request field rather than a llama-server restart: the same server answers
// both ways, and the token counts are what make the difference visible.
// Skipped unless WAVEZ_LLAMA_SERVER_URL names a running server.
func TestClient_Stream_ThinkingIsPerRequest(t *testing.T) {
	t.Parallel()

	baseURL := os.Getenv("WAVEZ_LLAMA_SERVER_URL")
	if baseURL == "" {
		t.Skip("WAVEZ_LLAMA_SERVER_URL not set; skipping live llama-server integration test")
	}

	client := openaic.New("llama-server", openaic.WithBaseURL(baseURL), openaic.WithModel("qwen3-8b"))

	on, off := true, false
	counts := map[string]int{}

	for name, thinking := range map[string]*bool{"on": &on, "off": &off} {
		req := llm.Request{
			Thinking: thinking,
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Reply with exactly: OK"}},
		}

		for chunk, err := range client.Stream(context.Background(), req) {
			if err != nil {
				t.Fatalf("thinking %s: stream error: %v", name, err)
			}
			if chunk.Kind == llm.ChunkDone && chunk.Usage != nil {
				counts[name] = chunk.Usage.OutputTokens
			}
		}
	}

	t.Logf("completion tokens: thinking on %d, off %d", counts["on"], counts["off"])

	if counts["off"] == 0 || counts["on"] <= counts["off"] {
		t.Fatalf("completion tokens on=%d off=%d, want the toggle to cut the count",
			counts["on"], counts["off"])
	}
}
