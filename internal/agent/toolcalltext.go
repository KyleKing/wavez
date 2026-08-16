package agent

import "strings"

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
