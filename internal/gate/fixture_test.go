package gate_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// copyFixtureModule copies testdata/fixture/<name> into a fresh temp dir so
// tests that run real `go` commands (go test, go list) never write into the
// repo's own testdata.
func copyFixtureModule(t *testing.T, name string) string {
	t.Helper()

	src := filepath.Join("testdata", "fixture", name)
	dst := t.TempDir()

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}
		if rel == "." {
			return nil
		}

		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		data, err := os.ReadFile(path) //nolint:gosec // path is walked from this package's own testdata fixture tree
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		//nolint:gosec // target is this test's own temp dir
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}

	return dst
}
