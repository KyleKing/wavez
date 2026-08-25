package agent

import (
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/xmlcall"
)

// toolCallMarkers are the wrappers models use when they render a tool call
// into prose instead of emitting one. They are the serialization formats
// providers parse out of the raw completion, so seeing one in the text
// means the provider's parser did not claim it.
//
// Measured against `qwen/qwen3-coder-30b-a3b-instruct` through OpenRouter
// (August 2026, provider SiliconFlow throughout, 15 samples per condition):
// with no system prompt the model emitted a native call 15/15; with this
// harness's system prompt, 0-4/15, the rest arriving as `<function=write>`
// in the message body. Upstream tracks the same failure as a chat-template
// weakness in QwenLM/Qwen3-Coder#475, where the model omits the opening
// `<tool_call>` tag most often when a call follows prose. Prompt wording
// moves the rate and does not remove it, so detection is not optional.
var toolCallMarkers = []string{
	"<function=",
	"<function_calls>",
	"<tool_call>",
	"<invoke name=",
	"<|tool_call",
}

// looksLikeToolCallText reports whether text renders a call to one of
// toolNames as markup rather than making it. A marker alone is not enough:
// prose may legitimately quote one (this package's own documentation does),
// so a registered tool name must follow the marker before the turn is
// treated as malformed.
func looksLikeToolCallText(text string, toolNames []string) bool {
	for _, marker := range toolCallMarkers {
		i := strings.Index(text, marker)
		if i < 0 {
			continue
		}

		rest := text[i+len(marker):]
		for _, name := range toolNames {
			if strings.Contains(rest, name) {
				return true
			}
		}
	}

	return false
}

// parseToolCallText recovers the calls a model rendered as
// `<function=name><parameter=key>value</parameter></function>`, which is
// Qwen's own serialization arriving unparsed.
//
// It is recovery and not repair: the model made a well-formed call in a
// dialect the provider failed to claim, so reading it costs nothing and
// invents nothing. Only a marker the detection above already fired on gets
// here, and only a name the registry holds is accepted, so the worst case
// is the refusal the run was already heading for.
//
// It exists because the coder-tuned model that does this project's tasks
// best is the one that leaks: measured over eight calls with the real tool
// surface and system prompt, `qwen/qwen3-coder-30b-a3b-instruct` emitted
// prose once, and that one call in eight was enough to end two of three
// `h6` runs.
func parseToolCallText(text string, toolNames []string) []llm.ToolCall {
	known := make(map[string]bool, len(toolNames))
	for _, n := range toolNames {
		known[n] = true
	}

	var calls []llm.ToolCall

	rest := text

	for {
		i := strings.Index(rest, "<function=")
		if i < 0 {
			break
		}

		rest = rest[i+len("<function="):]

		name, body, ok := strings.Cut(rest, ">")
		if !ok {
			break
		}

		name = strings.TrimSpace(name)

		block, tail, closed := strings.Cut(body, "</function>")
		if !closed {
			block, tail = body, ""
		}

		rest = tail

		if !known[name] {
			continue
		}

		calls = append(calls, llm.ToolCall{
			ID:    fmt.Sprintf("recovered-%d", len(calls)),
			Name:  name,
			Input: xmlcall.Parameters(block),
		})
	}

	return calls
}
