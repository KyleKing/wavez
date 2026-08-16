package tools_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/tools"
)

func openTestStore(t *testing.T) *codeintel.Store {
	t.Helper()

	store, err := codeintel.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return store
}

func TestSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mode        string
		query       string
		wantContent string
		wantIsError bool
	}{
		{name: "graph mode on an empty store", mode: "graph", query: "anything", wantContent: "no results"},
		{name: "unimplemented mode is an error result", mode: "semantic", query: "anything", wantIsError: true},
		{name: "empty query is an error result", mode: "fuzzy", query: "", wantIsError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tools.NewSearch(openTestStore(t))
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
