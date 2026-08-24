package agent

import (
	"encoding/json"
	"testing"
)

// A provider that does not parse the model's own serialization leaves a
// well-formed call sitting in the prose. Reading it invents nothing:
// measured over eight calls with the real tool surface and system prompt,
// `qwen/qwen3-coder-30b-a3b-instruct` emitted one, and one in eight was
// enough to end two of three `h6` runs.
func TestParseToolCallText(t *testing.T) {
	t.Parallel()

	const rendered = "I'll read the file first.\n\n" +
		"<function=read>\n<parameter=path>\ninternal/thread/thread.go\n</parameter>\n" +
		"<parameter=start_line>10</parameter>\n</function>\n</tool_call>"

	calls := parseToolCallText(rendered, []string{"read", "str_replace"})
	if len(calls) != 1 {
		t.Fatalf("parseToolCallText returned %d calls, want 1: %+v", len(calls), calls)
	}

	if calls[0].Name != "read" {
		t.Errorf("Name = %q, want read", calls[0].Name)
	}

	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
	}
	if err := json.Unmarshal(calls[0].Input, &args); err != nil {
		t.Fatalf("arguments do not parse as the schema reads them: %v", err)
	}

	if args.Path != "internal/thread/thread.go" {
		t.Errorf("path = %q, want the file it named", args.Path)
	}

	// A number arrives as text and has to reach the schema as a number.
	if args.StartLine != 10 {
		t.Errorf("start_line = %d, want 10", args.StartLine)
	}
}

// Only a name the registry holds is accepted, so prose quoting the markup
// (this package's own documentation does) recovers nothing.
func TestParseToolCallTextIgnoresWhatIsNotATool(t *testing.T) {
	t.Parallel()

	if got := parseToolCallText("<function=drop_database><parameter=x>1</parameter></function>",
		[]string{"read"}); len(got) != 0 {
		t.Errorf("parseToolCallText recovered %+v, want nothing", got)
	}

	if got := parseToolCallText("the marker is <function= and nothing follows", []string{"read"}); len(got) != 0 {
		t.Errorf("parseToolCallText recovered %+v from prose", got)
	}
}
