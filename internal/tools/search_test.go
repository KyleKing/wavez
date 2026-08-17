package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tools"
)

// openTestIndex builds an Indexer over a temp tree holding sources, keyed
// by relative path. An empty map gives a tree the index covers no files of,
// which is a distinct outcome from a query that misses.
func openTestIndex(t *testing.T, sources map[string]string) *codeintel.Indexer {
	t.Helper()

	root := t.TempDir()
	for rel, src := range sources {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	store, err := codeintel.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())
}

func TestSearch(t *testing.T) {
	t.Parallel()

	indexed := map[string]string{"greet.go": "package greet\n\nfunc Hello() string { return \"hi\" }\n"}

	tests := []struct {
		sources     map[string]string
		name        string
		mode        string
		query       string
		wantContent string
		wantIsError bool
	}{
		{
			name:        "a tree with nothing to index says so rather than reporting a miss",
			sources:     nil,
			mode:        "fuzzy",
			query:       "Hello",
			wantContent: "covers no files",
		},
		{
			name:        "a genuine miss names the query and what was searched",
			sources:     indexed,
			mode:        "fuzzy",
			query:       "Goodbye",
			wantContent: `no matches for "Goodbye" across 1 indexed files`,
		},
		{
			name:        "a hit comes back without any indexing having been asked for",
			sources:     indexed,
			mode:        "fuzzy",
			query:       "Hello",
			wantContent: "greet.go",
		},
		{name: "unimplemented mode is an error result", mode: "semantic", query: "anything", wantIsError: true},
		{name: "empty query is an error result", mode: "fuzzy", query: "", wantIsError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tools.NewSearch(openTestIndex(t, tt.sources))
			result, err := s.Run(context.Background(), mustJSON(t, map[string]any{"mode": tt.mode, "query": tt.query}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.IsError != tt.wantIsError {
				t.Errorf("IsError = %v, want %v (content=%q)", result.IsError, tt.wantIsError, result.Content)
			}

			if tt.wantContent != "" && !strings.Contains(result.Content, tt.wantContent) {
				t.Errorf("Content = %q, want it to contain %q", result.Content, tt.wantContent)
			}
		})
	}
}
