package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
