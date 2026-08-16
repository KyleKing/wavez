package codeintel_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
)

func TestOpen_MigratesFromEmpty(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	stats, err := store.Index(ctx, fixtureDir, defaultRegistry())
	if err != nil {
		t.Fatalf("Index on a freshly migrated store: %v", err)
	}
	if stats.FilesIndexed == 0 {
		t.Fatal("expected the migrated store to accept an index")
	}
}

func TestOpen_IdempotentReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")

	store, err := codeintel.Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := codeintel.Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	stats, err := reopened.Index(ctx, fixtureDir, defaultRegistry())
	if err != nil {
		t.Fatalf("Index after reopen: %v", err)
	}
	if stats.FilesIndexed != 0 || stats.FilesUnchanged != stats.FilesScanned {
		t.Errorf("reopen should see the prior index as unchanged, got %+v", stats)
	}
}

func TestOpen_RejectsNewerSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")

	store, err := codeintel.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version = 999`); err != nil {
		t.Fatalf("stamping future version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing raw db: %v", err)
	}

	if _, err := codeintel.Open(ctx, path); err == nil {
		t.Error("expected Open to reject a store from a newer schema version")
	}
}
