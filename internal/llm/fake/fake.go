// Package fake implements a scripted llm.Provider for deterministic tests.
package fake

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/llm"
)

// ErrScriptExhausted reports a Stream call past the end of the scripted turns.
var ErrScriptExhausted = errors.New("fake: script exhausted")

// Turn scripts one Stream call's output. Text and ToolCalls are emitted as
// chunks in order; a non-nil Err is yielded instead of a ChunkDone chunk,
// modeling a mid-stream provider failure. A ToolCall with invalid JSON Input
// models a malformed tool call, since the fake never validates it.
type Turn struct {
	Err        error
	Usage      *llm.Usage
	StopReason llm.StopReason
	Text       []string
	ToolCalls  []llm.ToolCall
	Delay      time.Duration
}

// Provider is a scripted llm.Provider that plays back an ordered list of
// Turn values, one per Stream call. It is safe for concurrent use.
type Provider struct {
	name     string
	script   []Turn
	requests []llm.Request
	next     int
	mu       sync.Mutex
}

// New builds a Provider named name that plays script in order, one Turn per
// Stream call. A Stream call past the end of script yields ErrScriptExhausted.
func New(name string, script ...Turn) *Provider {
	return &Provider{name: name, script: script}
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return p.name }

// Requests returns every Request Stream has received, in call order.
func (p *Provider) Requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.Request, len(p.requests))
	copy(out, p.requests)

	return out
}

// Stream implements llm.Provider, playing the next scripted Turn.
func (p *Provider) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		p.mu.Lock()
		idx := p.next
		p.next++
		p.requests = append(p.requests, req)
		p.mu.Unlock()

		if idx >= len(p.script) {
			yield(llm.Chunk{}, fmt.Errorf("%w: turn %d", ErrScriptExhausted, idx))
			return
		}
		turn := p.script[idx]

		for _, text := range turn.Text {
			if !emit(ctx, turn.Delay, llm.Chunk{Kind: llm.ChunkText, Text: text}, yield) {
				return
			}
		}
		for i := range turn.ToolCalls {
			tc := turn.ToolCalls[i]
			if !emit(ctx, turn.Delay, llm.Chunk{Kind: llm.ChunkToolCall, ToolCall: &tc}, yield) {
				return
			}
		}
		if turn.Err != nil {
			yield(llm.Chunk{}, turn.Err)
			return
		}
		yield(llm.Chunk{Kind: llm.ChunkDone, Usage: turn.Usage, StopReason: turn.StopReason}, nil)
	}
}

func emit(ctx context.Context, delay time.Duration, chunk llm.Chunk, yield func(llm.Chunk, error) bool) bool {
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			yield(llm.Chunk{}, ctx.Err())
			return false
		case <-timer.C:
		}
	} else {
		select {
		case <-ctx.Done():
			yield(llm.Chunk{}, ctx.Err())
			return false
		default:
		}
	}

	return yield(chunk, nil)
}
