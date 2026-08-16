package gate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/gate"
)

func openTestStore(t *testing.T) *codeintel.Store {
	t.Helper()

	store, err := codeintel.Open(context.Background(), filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close() //nolint:errcheck // best-effort cleanup, test already reported any real failure
	})

	return store
}

func TestCoverageAdapterRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := copyFixtureModule(t, "covmod")
	store := openTestStore(t)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	adapter := gate.NewCoverageAdapter(store, manifestPath, 2)

	assertFirstRunsEveryTest(ctx, t, adapter, store, repoRoot)
	assertSecondRunSkipsUnchangedFiles(ctx, t, adapter, repoRoot)
	assertTouchedFileForcesRerun(ctx, t, adapter, repoRoot)
}

func assertFirstRunsEveryTest(
	ctx context.Context, t *testing.T, adapter *gate.CoverageAdapter, store *codeintel.Store, repoRoot string,
) {
	t.Helper()

	stats, err := adapter.Refresh(ctx, repoRoot)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if stats.Considered != 2 || stats.Ran != 2 || stats.Skipped != 0 || stats.Failed != 0 {
		t.Fatalf("first Refresh stats = %+v, want Considered=2 Ran=2 Skipped=0 Failed=0", stats)
	}

	tests, err := store.CoveringTests(ctx, "greet.go", 1, 20)
	if err != nil {
		t.Fatalf("CoveringTests: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("CoveringTests(greet.go, 1-20) = %+v, want both covmod tests", tests)
	}
}

// assertSecondRunSkipsUnchangedFiles re-runs Refresh against a repo whose
// files did not change since the first run: the manifest's content hashes
// still match, so nothing should re-run.
func assertSecondRunSkipsUnchangedFiles(
	ctx context.Context, t *testing.T, adapter *gate.CoverageAdapter, repoRoot string,
) {
	t.Helper()

	stats, err := adapter.Refresh(ctx, repoRoot)
	if err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if stats.Ran != 0 || stats.Skipped != 2 {
		t.Fatalf("second Refresh stats = %+v, want Ran=0 Skipped=2 (nothing changed)", stats)
	}
}

func assertTouchedFileForcesRerun(ctx context.Context, t *testing.T, adapter *gate.CoverageAdapter, repoRoot string) {
	t.Helper()

	greetPath := filepath.Join(repoRoot, "greet.go")

	data, err := os.ReadFile(greetPath) //nolint:gosec // repoRoot is this test's own temp fixture copy
	if err != nil {
		t.Fatalf("reading greet.go: %v", err)
	}

	touched := append(append([]byte(nil), data...), []byte("\n// touched\n")...)

	//nolint:gosec // greetPath is this test's own temp fixture copy
	if err := os.WriteFile(greetPath, touched, 0o600); err != nil {
		t.Fatalf("touching greet.go: %v", err)
	}

	stats, err := adapter.Refresh(ctx, repoRoot)
	if err != nil {
		t.Fatalf("third Refresh: %v", err)
	}
	if stats.Ran != 2 || stats.Skipped != 0 {
		t.Fatalf("third Refresh stats = %+v, want Ran=2 Skipped=0 (greet.go's content hash changed)", stats)
	}
}
