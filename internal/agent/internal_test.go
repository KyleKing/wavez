package agent

import "testing"

// TestLooksLikeToolCallText is an internal test because the detector is not
// part of this package's API; it is the guard that keeps a turn which
// changed nothing from reporting success.
func TestLooksLikeToolCallText(t *testing.T) {
	t.Parallel()

	names := []string{"read", "str_replace", "write", "shell", "search", "question"}

	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "the shape observed from qwen3-coder-30b",
			text: "I'll create the file.\n\n<function=write>\n<parameter=path>\na.go\n</parameter>\n</function>",
			want: true,
		},
		{name: "xml tool_call wrapper", text: "<tool_call>\n{\"name\": \"shell\"}\n</tool_call>", want: true},
		{name: "anthropic-shaped block", text: "<function_calls>\n<invoke name=\"search\">", want: true},
		{name: "special-token wrapper", text: "<|tool_call_begin|>str_replace", want: true},
		{name: "an ordinary answer", text: "The lease TTL is configurable through Config.TTL.", want: false},
		{
			name: "markup naming no tool is prose, not a call",
			text: "The provider returns <function=…> in the body when its parser fails.",
			want: false,
		},
		{
			name: "a tool named without any marker is prose",
			text: "I used str_replace to change the constant, then ran shell.",
			want: false,
		},
		{name: "empty", text: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := looksLikeToolCallText(tt.text, names); got != tt.want {
				t.Errorf("looksLikeToolCallText(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
