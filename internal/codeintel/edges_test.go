package codeintel_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
)

// codegraphTree copies testdata/codegraph/src into a fresh temp directory.
func codegraphTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "codegraph", "src")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(root, rel), 0o750)
		}

		return copyFile(path, filepath.Join(root, rel))
	})
	if err != nil {
		t.Fatalf("copying codegraph fixture tree: %v", err)
	}

	return root
}

// codegraphFixture puts the recorded index (testdata/codegraph/graph.sql,
// the nodes and edges a real `codegraph init` produced over that same tree)
// where the adapter looks for it. It restores only the two tables the
// adapter reads, so codegraph itself cannot operate on the result.
func codegraphFixture(t *testing.T) string {
	t.Helper()
	root := codegraphTree(t)
	if err := os.MkdirAll(filepath.Join(root, ".codegraph"), 0o750); err != nil {
		t.Fatalf("creating .codegraph: %v", err)
	}
	recordedCodegraphDB(t, filepath.Join(root, ".codegraph"))

	return root
}

// recordedCodegraphDB writes testdata/codegraph/graph.sql into dir as a
// codegraph.db and returns its path.
func recordedCodegraphDB(t *testing.T, dir string) string {
	t.Helper()
	dump, err := os.ReadFile(filepath.Join("testdata", "codegraph", "graph.sql"))
	if err != nil {
		t.Fatalf("reading recorded graph: %v", err)
	}

	path := filepath.Join(dir, "codegraph.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("creating recorded index: %v", err)
	}
	defer func() { _ = db.Close() }() //nolint:errcheck // temp file under t.TempDir()
	if _, err := db.ExecContext(t.Context(), string(dump)); err != nil {
		t.Fatalf("loading recorded graph: %v", err)
	}

	return path
}

// fakeCodegraph writes an executable standing in for the codegraph binary,
// so init and sync are exercised without the real tool. Its `init` runs
// body (with $1 the target root) and its `sync` succeeds silently.
func fakeCodegraph(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codegraph")
	script := "#!/bin/sh\nset -eu\ncmd=\"$1\"\nshift\ncase \"$cmd\" in\ninit)\n" + body + "\n;;\nsync) ;;\n" +
		"*) echo \"unexpected: $cmd\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("writing fake codegraph: %v", err)
	}
	//nolint:gosec // a stand-in binary this test's own code executes
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("making fake codegraph executable: %v", err)
	}

	return path
}

// initCopyingRecordedGraph is a fake `codegraph init` that leaves the
// recorded index where a real one would land.
func initCopyingRecordedGraph(t *testing.T) string {
	t.Helper()
	db := recordedCodegraphDB(t, t.TempDir())

	return fakeCodegraph(t, "mkdir -p \"$1/.codegraph\"\ncp \""+db+"\" \"$1/.codegraph/codegraph.db\"")
}

func readGitignore(t *testing.T, root string) (string, bool) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, ".gitignore")) //nolint:gosec // root is t.TempDir()
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	return string(content), true
}

func indexedStore(t *testing.T, root string) (*codeintel.Store, context.Context) {
	t.Helper()
	store, ctx := openStore(t)
	if _, err := store.Index(ctx, root, lang.NewDefaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	return store, ctx
}

func TestEdgeAdapterCopy_RecordedGraph(t *testing.T) {
	t.Parallel()
	root := codegraphFixture(t)
	store, ctx := indexedStore(t, root)

	stats, err := codeintel.NewEdgeAdapter(root).Copy(ctx, store)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if stats.Unavailable != "" {
		t.Fatalf("Unavailable = %q, want empty", stats.Unavailable)
	}

	// The recording holds four adaptable edges. Three resolve; the fourth
	// references a package-level constant, which the Go extractor does not
	// emit as a symbol, so it must be counted rather than dropped silently.
	if stats.Read != 4 || stats.Copied != 3 || stats.Unresolved != 1 {
		t.Fatalf("stats = %+v, want Read 4 / Copied 3 / Unresolved 1", stats)
	}

	got, err := store.Search(ctx, codeintel.SearchQuery{
		Mode: codeintel.SearchGraph,
		Text: "calc/calc.go:Add:187",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := []codeintel.Edge{
		{Src: "calc/calc.go:Add:187", Dst: "calc/calc.go:scale:270", Kind: "calls", Confidence: 0.9},
		{Src: "calc/run.go:Run:113", Dst: "calc/calc.go:Add:187", Kind: "calls", Confidence: 0.9},
	}
	if len(got) != len(want) {
		t.Fatalf("graph search returned %d edges, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		e := got[i].Edge
		if e == nil {
			t.Fatalf("result %d carries no edge", i)
		}
		if e.Src != w.Src || e.Dst != w.Dst || e.Kind != w.Kind || e.Confidence != w.Confidence {
			t.Errorf("edge %d = %+v, want %+v", i, *e, w)
		}
	}
}

func TestEdgeAdapterCopy_ResolvesInstantiatesAndReplaces(t *testing.T) {
	t.Parallel()
	root := codegraphFixture(t)
	store, ctx := indexedStore(t, root)
	adapter := codeintel.NewEdgeAdapter(root)

	if _, err := adapter.Copy(ctx, store); err != nil {
		t.Fatalf("first Copy: %v", err)
	}
	second, err := adapter.Copy(ctx, store)
	if err != nil {
		t.Fatalf("second Copy: %v", err)
	}
	if second.Copied != 3 {
		t.Fatalf("second Copy wrote %d edges, want 3 (a copy replaces, never appends)", second.Copied)
	}

	bundle, err := store.Context(ctx, codeintel.ContextRequest{
		Touched: []codeintel.TouchedRange{{File: "calc/run.go", Start: 7, End: 14}},
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	kinds := make(map[string]bool)
	for _, e := range bundle.Neighbors {
		kinds[e.Kind] = true
	}
	if !kinds["calls"] || !kinds["instantiates"] {
		t.Fatalf("neighbors of Run = %+v, want both calls and instantiates", bundle.Neighbors)
	}
}

func TestEdgeAdapter_DegradesWithoutCodegraph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		root     func(t *testing.T) string
		binary   string
		wantPart string
	}{
		{
			name:     "no index in tree",
			root:     func(t *testing.T) string { t.Helper(); return copyFixture(t) },
			binary:   "codegraph",
			wantPart: "no codegraph index at",
		},
		{
			name:     "binary missing from PATH",
			root:     codegraphFixture,
			binary:   "wavez-no-such-codegraph",
			wantPart: "locating wavez-no-such-codegraph",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := tc.root(t)
			store, ctx := indexedStore(t, root)

			adapter := codeintel.NewEdgeAdapter(root, codeintel.WithCodegraphBinary(tc.binary))
			stats, err := adapter.Refresh(ctx, store)
			if err != nil {
				t.Fatalf("Refresh must not fail when codegraph is unusable: %v", err)
			}
			if !strings.Contains(stats.Unavailable, tc.wantPart) {
				t.Fatalf("Unavailable = %q, want it to mention %q", stats.Unavailable, tc.wantPart)
			}
			if stats.Copied != 0 {
				t.Fatalf("Copied = %d, want 0", stats.Copied)
			}

			results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchGraph, Text: "anything"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(results) != 0 {
				t.Fatalf("graph search returned %d edges, want 0", len(results))
			}
		})
	}
}

func TestIndexerRefreshEdges_GateOpensUntilACopySucceeds(t *testing.T) {
	t.Parallel()
	root := copyFixture(t)
	store, ctx := openStore(t)
	ix := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())

	first, err := ix.RefreshEdges(ctx)
	if err != nil {
		t.Fatalf("first RefreshEdges: %v", err)
	}
	if first.Unavailable == "" || first.Reused {
		t.Fatalf("first RefreshEdges = %+v, want an unavailable reason and no reuse", first)
	}

	second, err := ix.RefreshEdges(ctx)
	if err != nil {
		t.Fatalf("second RefreshEdges: %v", err)
	}
	if second.Reused {
		t.Fatal("a failed copy must not latch the gate, or `codegraph init` mid-session never takes effect")
	}
}

func TestIndexerRefreshEdges_ReusesUntilTheTreeMoves(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("codegraph"); err != nil {
		t.Skipf("codegraph not on PATH: %v", err)
	}
	root := codegraphTree(t)
	//nolint:gosec // root is this test's own t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "codegraph", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("codegraph init: %v: %s", err, out)
	}
	store, ctx := openStore(t)
	ix := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())

	first, err := ix.RefreshEdges(ctx)
	if err != nil {
		t.Fatalf("first RefreshEdges: %v", err)
	}
	if first.Unavailable != "" {
		t.Fatalf("Unavailable = %q, want a real copy", first.Unavailable)
	}
	if first.Copied == 0 {
		t.Fatal("Copied = 0, want the recorded graph copied")
	}

	second, err := ix.RefreshEdges(ctx)
	if err != nil {
		t.Fatalf("second RefreshEdges: %v", err)
	}
	if !second.Reused || second.Copied != first.Copied {
		t.Fatalf("second RefreshEdges = %+v, want the first stats reused", second)
	}

	extra := filepath.Join(root, "calc", "extra.go")
	if err := os.WriteFile(extra, []byte("package calc\n\nfunc Extra() int { return Run(nil) }\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", extra, err)
	}
	third, err := ix.RefreshEdges(ctx)
	if err != nil {
		t.Fatalf("third RefreshEdges: %v", err)
	}
	if third.Reused {
		t.Fatal("an edited tree must re-copy the graph")
	}
	if third.Copied <= first.Copied {
		t.Fatalf("Copied = %d after adding a caller, want more than %d", third.Copied, first.Copied)
	}
}

// reinit drops the index so the next InitAndRefresh runs `codegraph init`
// again over a project whose .gitignore already carries the entry.
func reinit(ctx context.Context, t *testing.T, adapter *codeintel.EdgeAdapter, store *codeintel.Store, root string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, ".codegraph")); err != nil {
		t.Fatalf("removing the index: %v", err)
	}
	if _, err := adapter.InitAndRefresh(ctx, store); err != nil {
		t.Fatalf("second InitAndRefresh: %v", err)
	}
}

// gitignoreCase is one starting state of a project's .gitignore, and what
// initializing codegraph in it must leave behind. An empty want means the
// file must come out byte-identical to existing.
type gitignoreCase struct {
	name     string
	existing string
	want     string
	absent   bool
}

func (tc gitignoreCase) run(t *testing.T) {
	t.Helper()
	want := tc.want
	if want == "" {
		want = tc.existing
	}

	root := codegraphTree(t)
	if !tc.absent {
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(tc.existing), 0o600); err != nil {
			t.Fatalf("seeding .gitignore: %v", err)
		}
	}
	store, ctx := indexedStore(t, root)
	adapter := codeintel.NewEdgeAdapter(root, codeintel.WithCodegraphBinary(initCopyingRecordedGraph(t)))

	stats, err := adapter.InitAndRefresh(ctx, store)
	if err != nil {
		t.Fatalf("InitAndRefresh: %v", err)
	}
	if stats.Unavailable != "" || stats.Copied != 3 {
		t.Fatalf("stats = %+v, want no reason and 3 edges copied", stats)
	}

	got, ok := readGitignore(t, root)
	if !ok {
		t.Fatal("no .gitignore written")
	}
	if got != want {
		t.Fatalf(".gitignore = %q, want %q", got, want)
	}

	reinit(ctx, t, adapter, store, root)
	if again, _ := readGitignore(t, root); again != want {
		t.Fatalf(".gitignore = %q after a second init, want it unchanged at %q", again, want)
	}
}

func TestEdgeAdapterInitAndRefresh_WritesGitignoreEntry(t *testing.T) {
	t.Parallel()

	tests := []gitignoreCase{
		{name: "created when the project has none", absent: true, want: ".codegraph/\n"},
		{name: "appended after existing entries", existing: "build/\n*.tmp\n", want: "build/\n*.tmp\n.codegraph/\n"},
		{name: "separated when the file lacks a trailing newline", existing: "build/", want: "build/\n.codegraph/\n"},
		{name: "left alone when the entry is present", existing: "a/\n.codegraph/\nb/\n"},
		{name: "left alone when a slashless entry covers it", existing: "# n\n.codegraph\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestEdgeAdapterInitAndRefresh_LeavesGitignoreAloneWhenAlreadyIndexed(t *testing.T) {
	t.Parallel()
	root := codegraphFixture(t)
	store, ctx := indexedStore(t, root)
	adapter := codeintel.NewEdgeAdapter(root, codeintel.WithCodegraphBinary(initCopyingRecordedGraph(t)))

	stats, err := adapter.InitAndRefresh(ctx, store)
	if err != nil {
		t.Fatalf("InitAndRefresh: %v", err)
	}
	if stats.Copied != 3 {
		t.Fatalf("Copied = %d, want 3", stats.Copied)
	}
	if _, ok := readGitignore(t, root); ok {
		t.Fatal("a project that already had an index must keep its .gitignore untouched")
	}
}

func TestEdgeAdapterInitAndRefresh_Degrades(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		binary        func(t *testing.T) string
		wantPart      string
		wantGitignore bool
	}{
		{
			name:     "binary missing from PATH",
			binary:   func(t *testing.T) string { t.Helper(); return "wavez-no-such-codegraph" },
			wantPart: "locating wavez-no-such-codegraph",
		},
		{
			name:          "init exits non-zero",
			binary:        func(t *testing.T) string { t.Helper(); return fakeCodegraph(t, "echo broken >&2; exit 3") },
			wantPart:      "codegraph init:",
			wantGitignore: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := codegraphTree(t)
			store, ctx := indexedStore(t, root)
			adapter := codeintel.NewEdgeAdapter(root, codeintel.WithCodegraphBinary(tc.binary(t)))

			stats, err := adapter.InitAndRefresh(ctx, store)
			if err != nil {
				t.Fatalf("InitAndRefresh must not fail when codegraph is unusable: %v", err)
			}
			if !strings.Contains(stats.Unavailable, tc.wantPart) {
				t.Fatalf("Unavailable = %q, want it to mention %q", stats.Unavailable, tc.wantPart)
			}
			if stats.Copied != 0 {
				t.Fatalf("Copied = %d, want 0", stats.Copied)
			}

			retry, err := adapter.InitAndRefresh(ctx, store)
			if err != nil || retry.Unavailable != stats.Unavailable {
				t.Fatalf("retry = %+v, %v, want the same reason rather than a latched failure", retry, err)
			}

			if _, ok := readGitignore(t, root); ok != tc.wantGitignore {
				t.Fatalf(".gitignore present = %v, want %v", ok, tc.wantGitignore)
			}
		})
	}
}

func TestIndexerRefreshEdges_ReportsAnIndexBeingBuilt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready, gate := filepath.Join(dir, "ready"), filepath.Join(dir, "gate")
	for _, fifo := range []string{ready, gate} {
		//nolint:gosec // fifo is a path under this test's own t.TempDir()
		if out, err := exec.CommandContext(t.Context(), "mkfifo", fifo).CombinedOutput(); err != nil {
			t.Skipf("mkfifo unavailable: %v: %s", err, out)
		}
	}

	root := codegraphTree(t)
	db := recordedCodegraphDB(t, t.TempDir())
	binary := fakeCodegraph(t, "echo ready > \""+ready+"\"\ncat < \""+gate+"\" > /dev/null\n"+
		"mkdir -p \"$1/.codegraph\"\ncp \""+db+"\" \"$1/.codegraph/codegraph.db\"")
	store, ctx := openStore(t)
	ix := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry(),
		codeintel.WithEdgeAdapter(codeintel.NewEdgeAdapter(root, codeintel.WithCodegraphBinary(binary))))

	built := make(chan codeintel.EdgeStats, 1)
	go func() {
		stats, err := ix.InitEdges(ctx)
		if err != nil {
			t.Errorf("InitEdges: %v", err)
		}
		built <- stats
	}()

	opened := make(chan error, 1)
	//nolint:gosec // ready is a fifo under this test's own t.TempDir()
	go func() { _, err := os.ReadFile(ready); opened <- err }()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("waiting for the fake init to start: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the fake `codegraph init` never started")
	}

	during, err := ix.RefreshEdges(ctx)
	if err != nil {
		t.Fatalf("RefreshEdges during a build: %v", err)
	}
	if !strings.Contains(during.Unavailable, "is being built") {
		t.Fatalf("Unavailable = %q, want a query during the build to say an index is on the way", during.Unavailable)
	}

	if err := os.WriteFile(gate, []byte("go\n"), 0o600); err != nil {
		t.Fatalf("releasing the fake init: %v", err)
	}
	stats := <-built
	if stats.Unavailable != "" || stats.Copied != 3 {
		t.Fatalf("InitEdges = %+v, want no reason and 3 edges copied", stats)
	}
}
