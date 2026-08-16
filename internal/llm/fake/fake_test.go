package fake_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
)

func collect(t *testing.T, p *fake.Provider, req llm.Request) ([]llm.Chunk, error) {
	t.Helper()
	var chunks []llm.Chunk
	for chunk, err := range p.Stream(context.Background(), req) {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

var errBoom = errors.New("boom")

func TestProvider_Stream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		name       string
		script     []fake.Turn
		wantChunks int
	}{
		{
			name: "text only",
			script: []fake.Turn{
				{Text: []string{"hello ", "world"}, StopReason: llm.StopEndTurn},
			},
			wantChunks: 3, // 2 text + done
		},
		{
			name: "tool call then done",
			script: []fake.Turn{
				{
					ToolCalls:  []llm.ToolCall{{ID: "1", Name: "str_replace", Input: json.RawMessage(`{"a":1}`)}},
					StopReason: llm.StopToolUse,
				},
			},
			wantChunks: 2, // tool call + done
		},
		{
			name: "malformed tool call input",
			script: []fake.Turn{
				{
					ToolCalls:  []llm.ToolCall{{ID: "1", Name: "str_replace", Input: json.RawMessage(`{not json`)}},
					StopReason: llm.StopToolUse,
				},
			},
			wantChunks: 2,
		},
		{
			name: "turn error",
			script: []fake.Turn{
				{Text: []string{"partial"}, Err: errBoom},
			},
			wantChunks: 1,
			wantErr:    errBoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := fake.New("test", tt.script...)
			chunks, err := collect(t, p, llm.Request{Model: "m"})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(chunks) != tt.wantChunks {
				t.Errorf("len(chunks) = %d, want %d", len(chunks), tt.wantChunks)
			}
		})
	}
}

func TestProvider_MalformedToolCallIsNotValidated(t *testing.T) {
	t.Parallel()
	p := fake.New("test", fake.Turn{
		ToolCalls: []llm.ToolCall{{ID: "1", Name: "x", Input: json.RawMessage(`{not json`)}},
	})
	chunks, err := collect(t, p, llm.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 2 || chunks[0].Kind != llm.ChunkToolCall {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
	var probe map[string]any
	if err := json.Unmarshal(chunks[0].ToolCall.Input, &probe); err == nil {
		t.Fatal("expected malformed tool call input to fail JSON decode")
	}
}

func TestProvider_ScriptExhausted(t *testing.T) {
	t.Parallel()
	p := fake.New("test", fake.Turn{Text: []string{"one turn"}})
	if _, err := collect(t, p, llm.Request{}); err != nil {
		t.Fatalf("first turn: unexpected error: %v", err)
	}
	_, err := collect(t, p, llm.Request{})
	if !errors.Is(err, fake.ErrScriptExhausted) {
		t.Fatalf("err = %v, want ErrScriptExhausted", err)
	}
}

func TestProvider_RecordsRequests(t *testing.T) {
	t.Parallel()
	p := fake.New("test", fake.Turn{Text: []string{"a"}}, fake.Turn{Text: []string{"b"}})
	if _, err := collect(t, p, llm.Request{Model: "one"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := collect(t, p, llm.Request{Model: "two"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := p.Requests()
	if len(got) != 2 || got[0].Model != "one" || got[1].Model != "two" {
		t.Fatalf("unexpected requests: %+v", got)
	}
}

func TestProvider_DelayRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	p := fake.New("test", fake.Turn{Text: []string{"slow"}, Delay: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var gotErr error
	for _, err := range p.Stream(ctx, llm.Request{}) {
		if err != nil {
			gotErr = err
			break
		}
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", gotErr)
	}
}

func TestProvider_ConcurrentUse(t *testing.T) {
	t.Parallel()
	const n = 20
	turns := make([]fake.Turn, n)
	for i := range turns {
		turns[i] = fake.Turn{Text: []string{"x"}, StopReason: llm.StopEndTurn}
	}
	p := fake.New("test", turns...)

	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if _, err := collect(t, p, llm.Request{}); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := len(p.Requests()); got != n {
		t.Fatalf("len(Requests()) = %d, want %d", got, n)
	}
}
