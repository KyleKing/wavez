package codeintel_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
)

var fixtureDir = filepath.Join("testdata", "fixture")

func openStore(t *testing.T) (*codeintel.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	store, err := codeintel.Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return store, ctx
}

// copyFixture copies testdata/fixture into a fresh temp directory so tests
// that mutate the tree (change or delete a file) never touch the checked-in
// fixture.
func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(fixtureDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking fixture: %w", err)
		}
		rel, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		return copyFile(path, target)
	})
	if err != nil {
		t.Fatalf("copying fixture: %v", err)
	}

	return dst
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src walks the fixed testdata/fixture tree, no untrusted input
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer func() { _ = in.Close() }() //nolint:errcheck // read-only handle

	out, err := os.Create(dst) //nolint:gosec // dst is under t.TempDir(), no untrusted input
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }() //nolint:errcheck // already returning the copy error below

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying %s to %s: %w", src, dst, err)
	}

	return nil
}

func defaultRegistry() *lang.Registry {
	return lang.NewDefaultRegistry()
}
