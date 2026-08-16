package codeintel_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
)

var update = flag.Bool("update", false, "update golden files")

func TestIndex_Golden(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	stats, err := store.Index(ctx, fixtureDir, defaultRegistry())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if stats.FilesIndexed != 2 {
		t.Fatalf("FilesIndexed = %d, want 2", stats.FilesIndexed)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "greet", Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	golden := filepath.Join("testdata", "symbols.golden")
	got := formatSymbolResults(results)
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatalf("writing golden: %v", err)
		}

		return
	}

	want, err := os.ReadFile(golden) //nolint:gosec // fixed path under testdata/, no untrusted input
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("symbols mismatch (-want +got):\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func formatSymbolResults(results []codeintel.SearchResult) string {
	var lines []string
	for _, r := range results {
		if r.Symbol == nil {
			continue
		}
		s := r.Symbol
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%d-%d\t%s\t%s",
			s.FilePath, s.Kind, s.Name, s.StartLine, s.EndLine, s.Signature, s.Doc))
	}
	sort.Strings(lines)

	return strings.Join(lines, "\n") + "\n"
}

func TestIndex_ReindexUnchangedIsNoOp(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	root := copyFixture(t)
	registry := defaultRegistry()

	first, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if first.FilesIndexed == 0 {
		t.Fatal("expected first index to index files")
	}

	second, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	want := codeintel.IndexStats{
		FilesScanned:   first.FilesScanned,
		FilesUnchanged: first.FilesScanned,
	}
	if second != want {
		t.Errorf("re-index of unchanged tree = %+v, want %+v", second, want)
	}
}

func TestIndex_ChangeOneFileTouchesOnlyItsRows(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	root := copyFixture(t)
	registry := defaultRegistry()

	if _, err := store.Index(ctx, root, registry); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	before, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "greeter", Limit: 100})
	if err != nil {
		t.Fatalf("Search before: %v", err)
	}
	pythonBefore := symbolIDsForFile(before, "pysrc/greeter.py")
	if len(pythonBefore) == 0 {
		t.Fatal("expected python symbols before change")
	}

	goFile := filepath.Join(root, "pkgone", "greeter.go")
	content, err := os.ReadFile(goFile) //nolint:gosec // goFile is built from t.TempDir(), no untrusted input
	if err != nil {
		t.Fatalf("reading go fixture: %v", err)
	}
	appended := string(content) + "\n// Farewell says goodbye.\nfunc Farewell() string {\n\treturn \"bye\"\n}\n"
	//nolint:gosec // goFile is built from t.TempDir(), no untrusted input
	if err := os.WriteFile(goFile, []byte(appended), 0o600); err != nil {
		t.Fatalf("writing go fixture: %v", err)
	}

	stats, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if stats.FilesIndexed != 1 || stats.FilesRemoved != 0 {
		t.Fatalf("stats = %+v, want FilesIndexed=1, FilesRemoved=0", stats)
	}

	after, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "greeter", Limit: 100})
	if err != nil {
		t.Fatalf("Search after: %v", err)
	}
	pythonAfter := symbolIDsForFile(after, "pysrc/greeter.py")
	if !equalInt64Sets(pythonBefore, pythonAfter) {
		t.Errorf("python symbol IDs changed: before %v, after %v", pythonBefore, pythonAfter)
	}

	farewell, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "farewell", Limit: 10})
	if err != nil {
		t.Fatalf("Search farewell: %v", err)
	}
	if len(farewell) == 0 {
		t.Error("expected the appended Farewell function to be indexed")
	}
}

func TestIndex_DeleteFileRemovesRowsAndFTS(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	root := copyFixture(t)
	registry := defaultRegistry()

	if _, err := store.Index(ctx, root, registry); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	if err := os.Remove(filepath.Join(root, "pysrc", "greeter.py")); err != nil {
		t.Fatalf("removing fixture file: %v", err)
	}

	stats, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("FilesRemoved = %d, want 1", stats.FilesRemoved)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "greeter", Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.File == "pysrc/greeter.py" {
			t.Errorf("expected no rows for deleted file, found %+v", r)
		}
	}
}

func symbolIDsForFile(results []codeintel.SearchResult, file string) []int64 {
	var ids []int64
	for _, r := range results {
		if r.Symbol != nil && r.Symbol.FilePath == file {
			ids = append(ids, r.Symbol.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return ids
}

func equalInt64Sets(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
