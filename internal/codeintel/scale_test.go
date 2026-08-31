package codeintel_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
)

// TestScale measures the index against a checkout large enough to be worth
// measuring, which no fixture in this repository is. It skips unless
// WAVEZ_SCALE_ROOT names one, so it costs nothing in CI and stays runnable
// by hand whenever a change here could move the numbers in
// docs/scale.md.
//
//	git clone --depth 1 --filter=blob:none https://github.com/getsentry/sentry ~/src/sentry
//	WAVEZ_SCALE_ROOT=~/src/sentry go test ./internal/codeintel -run TestScale -v
//
// It asserts nothing about wall-clock time, because the machine is as much
// of the measurement as the tree is. It fails only on a result a change here
// could break outright: a query that stops finding its own definition.
func TestScale(t *testing.T) {
	t.Parallel()

	root := os.Getenv("WAVEZ_SCALE_ROOT")
	if root == "" {
		t.Skip("WAVEZ_SCALE_ROOT unset; see this test's doc comment for the corpus")
	}

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "index.db")

	store, err := codeintel.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close() //nolint:errcheck // best-effort cleanup, the test already reported any real failure
	})

	stats, cold := indexToCompletion(ctx, t, store, root)
	t.Logf("cold %s over %d files, %d too large, file text indexed: %v",
		cold.Round(time.Millisecond), stats.FilesScanned, stats.FilesTooLarge, stats.ContentIndexed)
	t.Logf("warm re-index %s, which every query pays", warmReindex(ctx, t, store, root).Round(time.Millisecond))

	if fi, err := os.Stat(dbPath); err == nil {
		t.Logf("store %.1f MB", float64(fi.Size())/1e6)
	}

	searchEachQuery(ctx, t, store)
}

func indexToCompletion(
	ctx context.Context, t *testing.T, store *codeintel.Store, root string,
) (codeintel.IndexStats, time.Duration) {
	t.Helper()

	reg := lang.NewDefaultRegistry()

	var (
		last  codeintel.IndexStats
		total time.Duration
	)

	for range 32 {
		start := time.Now()

		stats, err := store.Index(ctx, root, reg)
		if err != nil {
			t.Fatalf("Index: %v", err)
		}

		total += time.Since(start)
		last = stats

		if stats.FilesDeferred == 0 {
			break
		}
	}

	return last, total
}

func warmReindex(ctx context.Context, t *testing.T, store *codeintel.Store, root string) time.Duration {
	t.Helper()

	const rounds = 3

	reg := lang.NewDefaultRegistry()

	var total time.Duration

	for range rounds {
		start := time.Now()

		if _, err := store.Index(ctx, root, reg); err != nil {
			t.Fatalf("re-index: %v", err)
		}

		total += time.Since(start)
	}

	return total / rounds
}

// searchEachQuery checks the property the whole index exists for: a symbol
// named in the query resolves to its own declaration. WAVEZ_SCALE_QUERIES
// is a comma-separated list of "symbol=path fragment" pairs.
func searchEachQuery(ctx context.Context, t *testing.T, store *codeintel.Store) {
	t.Helper()

	for _, pair := range strings.Split(os.Getenv("WAVEZ_SCALE_QUERIES"), ",") {
		name, wantPath, _ := strings.Cut(pair, "=")
		if name == "" {
			continue
		}

		start := time.Now()

		results, err := store.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: name, Limit: 10})
		if err != nil {
			t.Fatalf("Search %q: %v", name, err)
		}

		took := time.Since(start)

		if len(results) == 0 {
			t.Errorf("%q found nothing", name)

			continue
		}

		top := results[0]
		t.Logf("  %-30s %4s %2d hits, top %s", name, took.Round(time.Millisecond), len(results), top.File)

		if wantPath != "" && !strings.Contains(top.File, wantPath) {
			t.Errorf("%q ranked %s first, want a path holding %q", name, top.File, wantPath)
		}
	}
}
