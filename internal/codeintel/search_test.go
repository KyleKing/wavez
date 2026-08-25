package codeintel_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// A path, an operator, or a quote in the query would otherwise reach FTS5 as
// syntax and fail with "fts5: syntax error", which a model sees as a broken tool.
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

// FTS5 ANDs bare terms, so a caller naming several symbols got "no matches"
// for a query whose terms each exist in the index, one per file.
func TestSearch_FuzzyMatchesAnyTermNotEveryTerm(t *testing.T) {
	t.Parallel()

	store, ctx := openStore(t)
	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{
		Mode: codeintel.SearchFuzzy, Text: "NewGreeter new_greeter", Limit: 50,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("no results: each term exists in the index, in a different file")
	}
}

// A file hit that names a file and not a place in it leaves the caller to
// find the place, which on a dogfood run meant grep -rn over the file
// search had just found.
func TestSearch_FileHitCarriesTheMatchingLines(t *testing.T) {
	t.Parallel()

	store, ctx := openStore(t)
	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{
		Mode: codeintel.SearchFuzzy, Text: "Prefix", Limit: 50,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	source, err := os.ReadFile(filepath.Join(fixtureDir, "pkgone", "greeter.go"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	lines := strings.Split(string(source), "\n")

	var checked int
	for _, r := range results {
		if r.Kind != "file" || !strings.HasSuffix(r.File, "greeter.go") {
			continue
		}
		if len(r.Lines) == 0 {
			t.Fatalf("file hit on %s reported no lines", r.File)
		}
		checked += len(r.Lines)
		checkLineMatches(t, r, lines)
	}
	if checked == 0 {
		t.Fatal("no file-level hit on the fixture holding the term")
	}
}

func checkLineMatches(t *testing.T, r codeintel.SearchResult, lines []string) {
	t.Helper()

	for _, l := range r.Lines {
		if l.Line < 1 || l.Line > len(lines) {
			t.Fatalf("line %d is outside %s (%d lines)", l.Line, r.File, len(lines))
		}
		if got := strings.TrimSpace(lines[l.Line-1]); got != l.Text {
			t.Errorf("line %d text = %q, want %q", l.Line, l.Text, got)
		}
		if !strings.Contains(strings.ToLower(l.Text), "prefix") {
			t.Errorf("line %d %q does not hold the query term", l.Line, l.Text)
		}
	}
}

// bm25 over a trigram index scores by document length, so before ranking a
// short query answered with the shortest names that merely held its
// letters, and a documented function lost to them: on this repository
// `Read` put twelve names built from `Thread` above the two symbols
// actually called Read.
func TestSearch_FuzzyPutsNameMatchesAboveLetterMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := `package p

func OpenThread() {}
func threadClient() {}
func readTracker() {}
func newThread() {}
func parkThread() {}

// Read parses one recorded log and returns every entry it holds, which is
// the documentation that makes this the longest indexed document here.
func Read(path string, limit int) ([]string, error) { return nil, nil }
`
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	store, ctx := openStore(t)
	if _, err := store.Index(ctx, dir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "Read", Limit: 4})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var names []string

	for _, r := range results {
		if r.Symbol != nil {
			names = append(names, r.Symbol.Name)
		}
	}

	if len(names) == 0 || names[0] != "Read" {
		t.Errorf("fuzzy Read ranked %v, want the symbol named Read above the names that only share letters", names)
	}
}
