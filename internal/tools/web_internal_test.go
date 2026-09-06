package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kyleking/wavez/internal/web"
)

type stubSearcher struct{ urls []string }

func (s stubSearcher) Search(context.Context, string, int) ([]web.Result, error) {
	out := make([]web.Result, 0, len(s.urls))
	for _, u := range s.urls {
		out = append(out, web.Result{Title: "t", URL: u, Snippet: "s"})
	}

	return out, nil
}

// A result set is text somebody else wrote, so one hit on a host must not
// stand as provenance for every other page on it: a poisoned result would
// otherwise pre-approve the page it wanted read.
func TestWebSearchRecordsThePageAndNotTheSite(t *testing.T) {
	t.Parallel()

	search := &WebSearch{searcher: stubSearcher{urls: []string{"https://Docs.Example/a?v=1"}}, seen: newSeenURLs()}

	if _, err := search.Run(context.Background(), json.RawMessage(`{"query":"x"}`)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tests := []struct {
		url  string
		want bool
	}{
		{url: "https://docs.example/a?v=1", want: true},
		// The server never sees a fragment, so the same request matches.
		{url: "https://docs.example/a?v=1#top", want: true},
		{url: "https://docs.example/evil", want: false},
		{url: "https://docs.example/a", want: false},
		{url: "https://docs.example/a?v=2", want: false},
	}

	for _, tt := range tests {
		if got := search.seen.has(tt.url); got != tt.want {
			t.Errorf("has(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestPageKeyTreatsAnEmptyPathAsTheRoot(t *testing.T) {
	t.Parallel()

	seen := newSeenURLs()
	seen.add("https://example.com")

	if !seen.has("https://example.com/") {
		t.Error("a bare host and its root are not the same page")
	}
}
