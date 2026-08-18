package openaic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/kyleking/wavez/internal/llm"
)

const (
	maxErrorBodyBytes  = 4096
	sseScanInitialSize = 64 * 1024
	sseScanMaxSize     = 1 << 20
)

// Stream implements llm.Provider. It POSTs one SSE chat-completions request
// and yields chunks as they arrive: text deltas are yielded immediately, tool
// calls are assembled across fragmented deltas and yielded once the stream's
// finish reason arrives, and a ChunkDone chunk carrying usage and stop reason
// always closes a successful stream. Canceling ctx aborts the request and
// iteration stops before the next chunk.
func (c *Client) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		if c.baseURL == "" || c.model == "" {
			yield(llm.Chunk{}, fmt.Errorf("openaic: %s: %w", c.name, ErrNotConfigured))
			return
		}

		resp, err := c.do(ctx, req)
		if err != nil {
			yield(llm.Chunk{}, err)
			return
		}
		defer resp.Body.Close() //nolint:errcheck // nothing actionable once the stream has finished

		if statusErr := checkStatus(c.name, resp); statusErr != nil {
			yield(llm.Chunk{}, statusErr)
			return
		}

		state := newStreamState()
		if scanSSE(ctx, c.name, resp.Body, state, yield) {
			return
		}

		for _, tc := range state.toolCallChunks() {
			if !yield(llm.Chunk{Kind: llm.ChunkToolCall, ToolCall: tc}, nil) {
				return
			}
		}
		yield(llm.Chunk{Kind: llm.ChunkDone, Usage: state.usage, StopReason: state.stop}, nil)
	}
}

func checkStatus(name string, resp *http.Response) error {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	//nolint:errcheck // best-effort body for the error
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))

	return &StatusError{Provider: name, StatusCode: resp.StatusCode, Body: string(body)}
}

// scanSSE reads one SSE body to completion, driving yield for text chunks and
// accumulating tool call state in state. It returns true once the caller
// should stop iterating: an error was yielded, or the range consumer asked to
// stop early. A false return means the stream ended normally ("[DONE]" or
// EOF) and the caller should emit the accumulated tool calls and Done chunk.
func scanSSE(
	ctx context.Context, name string, body io.Reader, state *streamState, yield func(llm.Chunk, error) bool,
) bool {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, sseScanInitialSize), sseScanMaxSize)

	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		if data == "[DONE]" {
			return false
		}

		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			yield(llm.Chunk{}, fmt.Errorf("openaic: %s: decoding stream chunk: %w", name, err))
			return true
		}
		if processSSEChunk(name, chunk, state, yield) {
			return true
		}
	}

	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			yield(llm.Chunk{}, ctxErr)
		} else {
			yield(llm.Chunk{}, fmt.Errorf("openaic: %s: reading stream: %w", name, err))
		}

		return true
	}

	return false
}

// processSSEChunk applies one decoded event to state, yielding its text delta
// immediately. It returns true when iteration should stop: a mid-stream error
// payload, or the range consumer asking to stop early.
func processSSEChunk(name string, chunk sseChunk, state *streamState, yield func(llm.Chunk, error) bool) bool {
	if chunk.Error != nil {
		yield(llm.Chunk{}, &StreamError{
			Provider: name,
			Message:  chunk.Error.Message,
			Type:     chunk.Error.Type,
			Code:     chunk.Error.Code,
		})

		return true
	}
	if chunk.Usage != nil {
		state.usage = chunk.Usage.toLLMUsage()
	}
	if chunk.Timings != nil {
		state.applyTimings(chunk.Timings)
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			if !yield(llm.Chunk{Kind: llm.ChunkText, Text: choice.Delta.Content}, nil) {
				return true
			}
		}
		state.applyDelta(choice.Delta)
		if choice.FinishReason != nil {
			state.stop = mapFinishReason(*choice.FinishReason)
		}
	}

	return false
}

func (c *Client) do(ctx context.Context, req llm.Request) (*http.Response, error) {
	body, err := json.Marshal(toWireRequest(c.model, req))
	if err != nil {
		return nil, fmt.Errorf("openaic: %s: encoding request: %w", c.name, err)
	}

	endpoint := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaic: %s: building request: %w", c.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	key, err := c.resolveKey()
	if err != nil {
		return nil, err
	}
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaic: %s: %w", c.name, err)
	}

	return resp, nil
}

func sseData(line string) (string, bool) {
	const prefix = "data:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}

	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
}
