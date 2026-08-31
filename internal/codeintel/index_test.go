package codeintel_test

import (
	"bytes"
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
	if stats.FilesIndexed != 6 {
		t.Fatalf("FilesIndexed = %d, want 6", stats.FilesIndexed)
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
		ContentIndexed: true,
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

// A project's dependencies are not its code. Go keeps them outside the tree,
// so the first Python project the indexer met put 34,934 of its 35,888
// symbols in `.venv`: the symbol a run was looking for was indexed and
// unreachable under thousands of pytest and pluggy internals.
func TestIndex_SkipsTheDependencyDirectories(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	root := t.TempDir()
	registry := defaultRegistry()

	source := []byte("def only_mine():\n    return 1\n")
	// Each directory holds a file of its own, so dropping any single entry
	// from the skip list fails this rather than being covered by another.
	for _, dir := range []string{
		".", ".mypy_cache", ".pytest_cache", ".ruff_cache", ".tox", ".venv",
		"__pycache__", "node_modules", "site-packages", "vendor", "venv",
	} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o750); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}

		if err := os.WriteFile(filepath.Join(full, "mod.py"), source, 0o600); err != nil {
			t.Fatalf("writing into %s: %v", dir, err)
		}
	}

	stats, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if stats.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want only the project's own file", stats.FilesIndexed)
	}

	if stats.SymbolsIndexed != 1 {
		t.Errorf("SymbolsIndexed = %d, want only the project's own symbol", stats.SymbolsIndexed)
	}
}

// A file over the cap is passed over rather than indexed, and one that
// grows past it after being indexed has its rows removed like a deletion.
func TestIndex_PassesOverAFileOverTheSizeCap(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	root := t.TempDir()
	registry := defaultRegistry()
	path := filepath.Join(root, "mod.py")

	small := []byte("def only_mine():\n    return 1\n")
	if err := os.WriteFile(path, small, 0o600); err != nil {
		t.Fatalf("writing small: %v", err)
	}

	stats, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	if stats.FilesIndexed != 1 || stats.FilesTooLarge != 0 {
		t.Fatalf("indexed=%d toolarge=%d, want the small file indexed", stats.FilesIndexed, stats.FilesTooLarge)
	}

	padding := bytes.Repeat([]byte("# padding\n"), codeintel.MaxFileBytes/10+1)
	if err := os.WriteFile(path, append(small, padding...), 0o600); err != nil {
		t.Fatalf("writing large: %v", err)
	}

	stats, err = store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("Index after growth: %v", err)
	}

	if stats.FilesTooLarge != 1 {
		t.Errorf("FilesTooLarge = %d, want the grown file counted", stats.FilesTooLarge)
	}

	if stats.FilesRemoved != 1 {
		t.Errorf("FilesRemoved = %d, want the grown file's rows dropped", stats.FilesRemoved)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "only_mine", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Search found %d results in a file over the cap, want none", len(results))
	}
}

// A same-size rewrite that restores the original mtime is the one edit the
// stat gate cannot see, so the hash has to stay the authority whenever the
// stat says anything at all has moved.
func TestIndex_StatGateStillHashesWhenSizeOrTimeMoved(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	root := t.TempDir()
	registry := defaultRegistry()
	path := filepath.Join(root, "mod.py")

	if err := os.WriteFile(path, []byte("def alpha():\n    return 1\n"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	if _, err := store.Index(ctx, root, registry); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Same byte count, different content, so only the timestamp separates
	// them and the reindex has to follow it.
	if err := os.WriteFile(path, []byte("def gamma():\n    return 1\n"), 0o600); err != nil {
		t.Fatalf("rewriting: %v", err)
	}

	stats, err := store.Index(ctx, root, registry)
	if err != nil {
		t.Fatalf("Index after rewrite: %v", err)
	}

	if stats.FilesIndexed != 1 {
		t.Fatalf("FilesIndexed = %d, want the rewritten file reindexed", stats.FilesIndexed)
	}

	results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: "gamma", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Error("the rewritten symbol is not searchable, so the stat gate skipped a real edit")
	}
}

// The yak-shears miss, reproduced: a literal search for a class sitting in a
// stylesheet answered "no matches" because the index held no stylesheet, and
// the run read absence from the index as absence from the tree.
func TestIndex_FindsTextInAStylesheetAndATemplate(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	tests := map[string]string{
		"a class in a stylesheet":   "search-highlight",
		"an id in a template":       "search-results-container",
		"a jinja tag is still text": "endfor",
	}

	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			results, err := store.Search(ctx, codeintel.SearchQuery{
				Mode: codeintel.SearchLiteral, Text: query, Limit: 10,
			})
			if err != nil {
				t.Fatalf("Search(%q): %v", query, err)
			}

			if len(results) == 0 {
				t.Fatalf("Search(%q) found nothing, want the file holding it", query)
			}
		})
	}
}
