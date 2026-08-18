package openaic

import (
	"encoding/json"
	"strings"

	"github.com/kyleking/wavez/internal/llm"
)

type toolCallBuilder struct {
	id   string
	name string
	args strings.Builder
}

// streamState accumulates the parts of a response that only make sense once
// the stream finishes: tool call arguments assembled across deltas, usage,
// and the stop reason. Text deltas bypass this and are yielded immediately.
type streamState struct {
	toolCalls map[int]*toolCallBuilder
	usage     *llm.Usage
	stop      llm.StopReason
	order     []int
}

func newStreamState() *streamState {
	return &streamState{toolCalls: map[int]*toolCallBuilder{}, stop: llm.StopEndTurn}
}

func (s *streamState) applyDelta(delta sseDelta) {
	for _, tc := range delta.ToolCalls {
		b, ok := s.toolCalls[tc.Index]
		if !ok {
			b = &toolCallBuilder{}
			s.toolCalls[tc.Index] = b
			s.order = append(s.order, tc.Index)
		}
		if tc.ID != "" {
			b.id = tc.ID
		}
		if tc.Function.Name != "" {
			b.name = tc.Function.Name
		}
		b.args.WriteString(tc.Function.Arguments)
	}
}

// applyTimings attaches a runtime's own measurement of the call. It arrives
// on the same chunk as usage but is independent of it, so a stream carrying
// timings and no usage still reports decode speed.
func (s *streamState) applyTimings(t *sseTimings) {
	if s.usage == nil {
		s.usage = &llm.Usage{}
	}

	s.usage.Timings = t.toLLMTimings()
}

func (s *streamState) toolCallChunks() []*llm.ToolCall {
	out := make([]*llm.ToolCall, 0, len(s.order))
	for _, idx := range s.order {
		b := s.toolCalls[idx]
		out = append(out, &llm.ToolCall{ID: b.id, Name: b.name, Input: json.RawMessage(b.args.String())})
	}

	return out
}
