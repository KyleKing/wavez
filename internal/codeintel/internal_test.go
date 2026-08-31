package codeintel

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/codeintel/lang"
)

// The first pass holds the walk lock for as long as the tree takes, which on
// a 244 MB checkout is 14.5 seconds. Holding mu here stands in for that: a
// Refresh that took the lock would deadlock this rather than fail it.
func TestIndexerRefresh_DoesNotWaitOutTheFirstPass(t *testing.T) {
	t.Parallel()

	ix := &Indexer{}
	ix.building.Store(true)
	ix.mu.Lock()
	defer ix.mu.Unlock()

	stats, err := ix.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh during the first pass: %v", err)
	}

	if stats.Building == "" {
		t.Fatal("Building is empty, want a query during the first pass told the answer is partial")
	}
}

// The bypass is the flag and nothing else, so a Refresh after the first pass
// serializes with whatever holds the walk.
func TestIndexerRefresh_WaitsOnceTheFirstPassIsDone(t *testing.T) {
	t.Parallel()

	ix := &Indexer{}
	ix.mu.Lock()

	returned := make(chan struct{})
	go func() {
		defer close(returned)

		_, _ = ix.Refresh(t.Context()) //nolint:errcheck // the call is expected to block, not to answer
	}()

	select {
	case <-returned:
		t.Fatal("Refresh answered while the walk lock was held, so the flag is not what bypasses it")
	case <-time.After(100 * time.Millisecond):
	}

	ix.mu.Unlock()
	<-returned
}

// A pass that has spent its budget leaves the file for the next one rather
// than parsing it, and an unchanged file never spends the budget at all,
// which together are what make successive passes advance.
func TestIndexRun_DefersOnceThePassBudgetIsSpent(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.py")

	if err := os.WriteFile(path, []byte("def only_mine():\n    return 1\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	store, err := Open(ctx, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close() //nolint:errcheck // best-effort cleanup, the test already reported any real failure
	})

	for _, tc := range []struct {
		name     string
		spent    int
		indexed  int
		deferred int
	}{
		{name: "within the budget", spent: 0, indexed: 1, deferred: 0},
		{name: "budget spent", spent: MaxIndexBytesPerPass, indexed: 0, deferred: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stats IndexStats

			err := store.withWrite(ctx, func(tx *sql.Tx) error {
				run := &indexRun{
					tx: tx, registry: lang.NewDefaultRegistry(),
					existing: map[string]File{}, stats: &stats, indexed: tc.spent,
				}

				return run.indexOneFile(ctx, scannedFile{relPath: "mod.py", absPath: path})
			})
			if err != nil {
				t.Fatalf("indexOneFile: %v", err)
			}

			if stats.FilesIndexed != tc.indexed || stats.FilesDeferred != tc.deferred {
				t.Errorf("indexed=%d deferred=%d, want %d and %d",
					stats.FilesIndexed, stats.FilesDeferred, tc.indexed, tc.deferred)
			}
		})
	}
}

// A project that grows past MaxContentIndexBytes has to lose the whole-file
// rows a smaller version of it left behind, or a literal query keeps being
// answered from whichever files happened to be indexed before it crossed,
// which reads as a complete answer and is not one.
func TestIndex_CrossingTheContentThresholdDropsTheFileRows(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")

	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("creating the tree: %v", err)
	}

	source := "def alpha():\n    return \"a distinctive phrase\"\n"
	if err := os.WriteFile(filepath.Join(root, "mod.py"), []byte(source), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	store, err := Open(ctx, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close() //nolint:errcheck // best-effort cleanup, the test already reported any real failure
	})

	reg := lang.NewDefaultRegistry()
	if _, err := store.Index(ctx, root, reg); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if n := countFileRows(ctx, t, store); n != 1 {
		t.Fatalf("whole-file fts rows = %d under the threshold, want 1", n)
	}

	err = store.withWrite(ctx, func(tx *sql.Tx) error {
		return (&indexRun{tx: tx}).dropContentRows(ctx)
	})
	if err != nil {
		t.Fatalf("dropContentRows: %v", err)
	}

	if n := countFileRows(ctx, t, store); n != 0 {
		t.Errorf("whole-file fts rows = %d over the threshold, want none", n)
	}
}

func countFileRows(ctx context.Context, t *testing.T, store *Store) int {
	t.Helper()

	var n int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM fts WHERE kind = 'file'`).Scan(&n); err != nil {
		t.Fatalf("counting whole-file fts rows: %v", err)
	}

	return n
}
