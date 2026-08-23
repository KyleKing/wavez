package bench_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// The counts this asserts are the ones a lane is judged by, so the shape of
// the log they are read from matters as much as the arithmetic: usage rides
// on the turn marker, a tool call carries its own name, and an edit is any
// tool result that named a change.
func TestSummarizeCountsWhatARunSpent(t *testing.T) {
	t.Parallel()

	path := writeLog(t, []event.Event{
		turn("balanced", 1000, 40, 900),
		read("a.go", strings.Repeat("x", 500)),
		read("b.go", strings.Repeat("y", 300)),
		read("a.go", strings.Repeat("x", 500)),
		{Kind: event.KindTool, Tool: "search", Text: "no matches for \"q\" across 12 indexed files"},
		edit("a.go"),
		read("a.go", strings.Repeat("x", 500)),
		turn("deep", 2000, 60, 0),
		{Kind: event.KindGate, Detail: map[string]any{"round": 1, "pass": false}},
		{Kind: event.KindReview, Detail: map[string]any{"round": 1, "result": "objection"}},
		{Kind: event.KindUsage, Detail: map[string]any{"tokens_saved": 128}},
	})

	events, err := bench.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got := bench.Summarize(events)

	checks := []struct {
		name string
		got  int
		want int
	}{
		{"Turns", got.Turns, 2},
		{"ToolCalls", got.ToolCalls, 6},
		{"InputTokens", got.InputTokens, 3000},
		{"OutputTokens", got.OutputTokens, 100},
		{"CacheReadTokens", got.CacheReadTokens, 900},
		{"RepeatReads", got.RepeatReads, 1},
		{"RepeatReadBytes", got.RepeatReadBytes, 500},
		{"EmptySearches", got.EmptySearches, 1},
		{"GateFailures", got.GateFailures, 1},
		{"ReviewObjections", got.ReviewObjections, 1},
		{"CompactionSaved", got.CompactionSaved, 128},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	if got.TierTurns["balanced"] != 1 || got.TierTurns["deep"] != 1 {
		t.Errorf("TierTurns = %v, want one turn each on balanced and deep", got.TierTurns)
	}

	if len(got.Tools) == 0 || got.Tools[0].Name != "read" || got.Tools[0].Calls != 4 {
		t.Errorf("Tools[0] = %+v, want read with 4 calls first", got.Tools)
	}
}

// RenderJSON must carry the counts Render prints, under the snake_case names
// a diff script reads, so a round trip through the writer has to land on the
// same numbers Summarize produced.
func TestRenderJSONRoundTripsTheCounts(t *testing.T) {
	t.Parallel()

	path := writeLog(t, []event.Event{
		turn("balanced", 1000, 40, 900),
		read("a.go", strings.Repeat("x", 500)),
		read("a.go", strings.Repeat("x", 500)),
	})

	events, err := bench.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	var out strings.Builder
	if err := bench.Summarize(events).RenderJSON(&out); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	// Unmarshaling into a map is what catches a wrong tag: the decoder would
	// otherwise match input_tokens to InputTokens by field name alone.
	var wire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &wire); err != nil {
		t.Fatalf("Unmarshal %s: %v", out.String(), err)
	}

	if _, ok := wire["input_tokens"]; !ok {
		t.Errorf("wire has no input_tokens key, got %s", out.String())
	}

	var decoded bench.Stats
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatalf("Unmarshal into Stats: %v", err)
	}

	if decoded.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", decoded.InputTokens)
	}

	if decoded.RepeatReads != 1 || decoded.RepeatReadBytes != 500 {
		t.Errorf("RepeatReads = %d, RepeatReadBytes = %d, want 1 and 500",
			decoded.RepeatReads, decoded.RepeatReadBytes)
	}
}

// Compare must show what moved between two runs and leave what did not at
// zero: a changed field carries its signed delta and an unchanged field
// prints +0, so diffing this output against another comparison is exact.
func TestCompareShowsSignedDeltaPerField(t *testing.T) {
	t.Parallel()

	before := bench.Stats{
		Turns:       4,
		ToolCalls:   12,
		InputTokens: 3000,
	}
	after := bench.Stats{
		Turns:       6,
		ToolCalls:   9,
		InputTokens: 3000,
	}

	var out strings.Builder
	if err := bench.Compare(before, after, &out); err != nil {
		t.Fatalf("Compare: %v", err)
	}

	// Fields, not whole lines: the columns are padding, what matters is that
	// each row reads label, baseline value, current value, signed delta.
	checks := []struct {
		label string
		want  []string
	}{
		{"turns", []string{"turns", "4", "6", "+2"}},
		{"tool calls", []string{"tool", "calls", "12", "9", "-3"}},
		{"input tokens", []string{"input", "tokens", "3000", "3000", "+0"}},
	}
	for _, c := range checks {
		line, ok := lineFor(out.String(), c.label)
		if !ok {
			t.Errorf("Compare output has no %q row, got:\n%s", c.label, out.String())

			continue
		}

		if got := strings.Fields(line); !slices.Equal(got, c.want) {
			t.Errorf("%s row = %v, want %v", c.label, got, c.want)
		}
	}

	if lines := strings.Count(out.String(), "\n"); lines != 13 {
		t.Errorf("Compare wrote %d lines, want 13", lines)
	}
}

// lineFor returns the comparison row beginning with the given label's words.
func lineFor(report, label string) (string, bool) {
	words := strings.Fields(label)

	for _, line := range strings.Split(report, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= len(words) && slices.Equal(fields[:len(words)], words) {
			return line, true
		}
	}

	return "", false
}

func turn(tier string, in, out, cached int) event.Event {
	return event.Event{Kind: event.KindAgent, Role: event.RoleNote, Detail: map[string]any{
		"tier": tier,
		"usage": map[string]any{
			"input_tokens": in, "output_tokens": out, "cache_read_tokens": cached,
		},
	}}
}

func read(path, content string) event.Event {
	return event.Event{
		Kind: event.KindTool, Tool: "read", Text: content,
		Detail: map[string]any{"input": `{"path":"` + path + `"}`},
	}
}

func edit(path string) event.Event {
	return event.Event{
		Kind: event.KindTool, Tool: "str_replace", Text: "ok",
		Detail:  map[string]any{"input": `{"path":"` + path + `"}`},
		Changes: []tool.Change{{Path: path}},
	}
}

func writeLog(t *testing.T, events []event.Event) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "thread.jsonl")

	var b strings.Builder
	for i := range events {
		line, err := json.Marshal(events[i])
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		b.Write(line)
		b.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

// shell builds one logged shell call whose input is the raw JSON string the
// model sent, the form the log stores and Summarize reads the command from.
func shell(command string, resultBytes int) event.Event {
	return event.Event{
		Kind: event.KindTool, Tool: "shell",
		Text:   strings.Repeat("z", resultBytes),
		Detail: map[string]any{"input": fmt.Sprintf(`{"command":%q}`, command)},
	}
}

// An error result must be counted for its own tool alone: the run's total
// rises by one and so does the failing tool's count, while a successful call
// adds to neither.
func TestSummarizeCountsErrorResultsPerTool(t *testing.T) {
	t.Parallel()

	path := writeLog(t, []event.Event{
		{
			Kind: event.KindTool, Tool: "shell", Text: "command failed",
			Detail: map[string]any{"is_error": true},
		},
		{Kind: event.KindTool, Tool: "read", Text: "file contents"},
	})

	events, err := bench.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got := bench.Summarize(events)

	if got.ErrorResults != 1 {
		t.Errorf("ErrorResults = %d, want 1", got.ErrorResults)
	}

	errorsOf := func(name string) int {
		for _, ts := range got.Tools {
			if ts.Name == name {
				return ts.Errors
			}
		}

		return -1
	}
	if n := errorsOf("shell"); n != 1 {
		t.Errorf("shell Errors = %d, want 1", n)
	}
	if n := errorsOf("read"); n != 0 {
		t.Errorf("read Errors = %d, want 0", n)
	}
}

// Summarize must read each shell command out of the raw input JSON a tool
// event carries and pair it with that call's own result size, so Render can
// show which commands produced the most output.
func TestSummarizeExtractsShellCommandsWithResultSizes(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("go build ./... && ", 10)

	path := writeLog(t, []event.Event{
		turn("balanced", 1000, 40, 900),
		read("a.go", strings.Repeat("x", 500)),
		shell(long, 4000),
		shell("mise run ci", 12000),
		{Kind: event.KindTool, Tool: "search", Text: "no matches", Detail: map[string]any{
			"input": `{"query":"q"}`,
		}},
	})

	events, err := bench.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got := bench.Summarize(events)

	want := []bench.ShellCmd{
		{Command: long, ResultBytes: 4000},
		{Command: "mise run ci", ResultBytes: 12000},
	}
	if !slices.Equal(got.ShellCmds, want) {
		t.Fatalf("ShellCmds = %+v, want %+v", got.ShellCmds, want)
	}

	var out strings.Builder
	if err := got.Render(&out); err != nil {
		t.Fatalf("Render: %v", err)
	}

	report := out.String()
	if !strings.Contains(report, "\nshell commands by result size\n") {
		t.Fatalf("Render has no shell commands heading, got:\n%s", report)
	}

	if strings.Index(report, "12000 bytes") > strings.Index(report, "4000 bytes") {
		t.Errorf("Render orders commands by result size, got:\n%s", report)
	}

	for _, line := range strings.Split(report, "\n") {
		cmd, found := strings.CutPrefix(line, "      4000 bytes  ")
		if !found {
			continue
		}

		if n := len([]rune(cmd)); n > 100 {
			t.Errorf("command is %d characters, want at most 100: %q", n, cmd)
		}

		if !strings.HasSuffix(cmd, "…") || strings.ContainsRune(cmd, '\n') {
			t.Errorf("command is not one truncated line: %q", cmd)
		}
	}
}
