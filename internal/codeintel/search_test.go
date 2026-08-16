package codeintel_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
)

func TestSearch_FuzzyIsDeterministic(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	query := codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "greet", Limit: 50}
	first, err := store.Search(ctx, query)
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	second, err := store.Search(ctx, query)
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("identical fuzzy queries returned different results:\n%+v\n%+v", first, second)
	}
	if len(first) == 0 {
		t.Fatal("expected fuzzy search for \"greet\" to return results")
	}
}

func TestSearch_UnimplementedModes(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	for _, mode := range []codeintel.SearchMode{codeintel.SearchSemantic, codeintel.SearchHybrid} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			_, err := store.Search(ctx, codeintel.SearchQuery{Mode: mode, Text: "x"})
			if !errors.Is(err, codeintel.ErrModeUnimplemented) {
				t.Errorf("Search(mode=%s) error = %v, want ErrModeUnimplemented", mode, err)
			}
		})
	}
}

func TestSearch_GraphModeQueriesEdges(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchGraph, Text: "anything"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no edges before the codegraph adapter runs, got %d", len(results))
	}
}

// A path, an operator, or a quote in the query used to reach FTS5 as syntax and
// fail with "fts5: syntax error", which a model sees as a broken tool.
func TestSearchAcceptsQueriesThatLookLikeFTSSyntax(t *testing.T) {
	t.Parallel()

	store, ctx := openStore(t)

	queries := []string{
		"internal/lease",
		`"unbalanced`,
		"foo AND bar",
		"a-b*c",
		"NEAR(x y)",
		"...",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			t.Parallel()
			if _, err := store.Search(ctx, codeintel.SearchQuery{
				Mode: codeintel.SearchFuzzy, Text: q,
			}); err != nil {
				t.Fatalf("Search(%q) failed: %v", q, err)
			}
		})
	}
}
