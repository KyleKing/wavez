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
