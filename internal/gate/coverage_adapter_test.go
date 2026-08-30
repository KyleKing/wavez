package gate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
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

	adapter := gate.NewCoverageAdapter(store, repoRoot, manifestPath, 2)

	if adapter.CoverageReady() {
		t.Fatal("CoverageReady before any build = true, want false: an unbuilt map must not answer selection")
	}

	assertFirstRunsEveryTest(ctx, t, adapter, store)

	if !adapter.CoverageReady() {
		t.Fatal("CoverageReady after a full build = false, want true")
	}

	assertSecondRunSkipsUnchangedFiles(ctx, t, adapter)
	assertTouchedFileForcesRerun(ctx, t, adapter, repoRoot)
	assertRemovedTestLeavesNoCoverage(ctx, t, adapter, store, repoRoot)

	// A map another process already finished is usable without rebuilding it.
	if !gate.NewCoverageAdapter(store, repoRoot, manifestPath, 1).CoverageReady() {
		t.Error("CoverageReady on a fresh adapter over a complete manifest = false, want true")
	}
}

// TestCoverageAdapterIncompleteBuildStaysUnready pins the distinction
// selection depends on: a manifest holding some tests is a partial map, and
// a partial map must report itself unready rather than let selection read
// its silence as "no test covers this".
func TestCoverageAdapterIncompleteBuildStaysUnready(t *testing.T) {
	t.Parallel()

	repoRoot := copyFixtureModule(t, "covmod")
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	partial := `{"tests":{"covmod.TestGreet":{"greet.go":"deadbeef"}},"complete":false}`
	if err := os.WriteFile(manifestPath, []byte(partial), 0o600); err != nil {
		t.Fatalf("writing partial manifest: %v", err)
	}

	adapter := gate.NewCoverageAdapter(openTestStore(t), repoRoot, manifestPath, 1)
	if adapter.CoverageReady() {
		t.Fatal("CoverageReady over a partial manifest = true, want false")
	}
}

// A build that runs out of budget defers the rest rather than measuring a
// large module for an hour, and the map it leaves behind is resumable: the
// next build takes the tests the first one never reached.
func TestCoverageAdapterBudgetDefersTheRestAndResumes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := copyFixtureModule(t, "covmod")
	store := openTestStore(t)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	spent := gate.NewCoverageAdapter(store, repoRoot, manifestPath, 2, gate.WithCoverageBudget(0))

	stats, err := spent.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh with no budget: %v", err)
	}

	if stats.Ran != 0 || stats.Deferred != stats.Considered {
		t.Fatalf("Ran = %d, Deferred = %d of %d considered, want every test deferred",
			stats.Ran, stats.Deferred, stats.Considered)
	}

	if spent.CoverageReady() {
		t.Fatal("CoverageReady after a build that measured nothing = true, want false")
	}

	resumed := gate.NewCoverageAdapter(store, repoRoot, manifestPath, 2)

	stats, err = resumed.Refresh(ctx)
	if err != nil {
		t.Fatalf("Refresh with a budget: %v", err)
	}

	if stats.Ran != stats.Considered || stats.Deferred != 0 {
		t.Fatalf("Ran = %d, Deferred = %d of %d, want the deferred tests taken",
			stats.Ran, stats.Deferred, stats.Considered)
	}

	if !resumed.CoverageReady() {
		t.Error("CoverageReady after the resumed build = false, want true")
	}
}

// TestCoverageMapDrivesLineLevelSelection is the end-to-end path DESIGN.md
// ships in M1: a built map resolves a change to the tests that cover it,
// the go-test gate runs exactly those, and the gate log records the level
// it ran at.
func TestCoverageMapDrivesLineLevelSelection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := copyFixtureModule(t, "covmod")
	logPath := filepath.Join(t.TempDir(), "gate.log")

	gateLog, err := gate.OpenLog(logPath)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	adapter := gate.NewCoverageAdapter(openTestStore(t), repoRoot,
		filepath.Join(t.TempDir(), "manifest.json"), 2)

	if _, err := adapter.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	runFn := gate.BuildRunFunc(gate.RealClock{}, adapter, nil,
		[]gate.Gate{gate.NewGoTestGate(repoRoot)}, gateLog, repoRoot, gate.NewResourceSet())

	res := runFn(ctx, []tool.Change{{Path: "greet.go", Ranges: []tool.LineRange{{Start: 3, End: 8}}}})
	if len(res.Gates) != 1 {
		t.Fatalf("got %d gate results, want 1", len(res.Gates))
	}
	if got := res.Gates[0]; got.Level != gate.LevelLine || !got.Pass || got.Examined != 2 {
		t.Fatalf("gate result = %+v, want line level, passing, 2 tests examined", got)
	}

	entries, err := gateLog.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	levels := make([]gate.Level, 0, len(entries))
	for _, e := range entries {
		levels = append(levels, e.Level)
	}

	if len(levels) != 1 || levels[0] != gate.LevelLine {
		t.Fatalf("gate log levels = %v, want exactly one %q entry", levels, gate.LevelLine)
	}
}

func assertFirstRunsEveryTest(
	ctx context.Context, t *testing.T, adapter *gate.CoverageAdapter, store *codeintel.Store,
) {
	t.Helper()

	stats, err := adapter.Refresh(ctx)
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

// assertRemovedTestLeavesNoCoverage deletes a test and re-runs: its rows
// have to go with it, or selection keeps naming a test `go test -run` can
// never match.
func assertRemovedTestLeavesNoCoverage(
	ctx context.Context, t *testing.T, adapter *gate.CoverageAdapter, store *codeintel.Store, repoRoot string,
) {
	t.Helper()

	kept := "package covmod\n\nimport \"testing\"\n\n" +
		"func TestGreetNamed(t *testing.T) {\n" +
		"\tif got := Greet(\"Ada\"); got != \"hello, Ada\" {\n\t\tt.Fatalf(\"got %q\", got)\n\t}\n}\n"

	if err := os.WriteFile(filepath.Join(repoRoot, "greet_test.go"), []byte(kept), 0o600); err != nil {
		t.Fatalf("rewriting greet_test.go: %v", err)
	}

	if _, err := adapter.Refresh(ctx); err != nil {
		t.Fatalf("Refresh after removing a test: %v", err)
	}

	tests, err := store.CoveringTests(ctx, "greet.go", 1, 20)
	if err != nil {
		t.Fatalf("CoveringTests: %v", err)
	}

	for _, tc := range tests {
		if tc.TestID == "covmod.TestGreetEmpty" {
			t.Fatalf("CoveringTests still names the deleted test: %+v", tests)
		}
	}
}

// assertSecondRunSkipsUnchangedFiles re-runs Refresh against a repo whose
// files did not change since the first run: the manifest's content hashes
// still match, so nothing should re-run.
func assertSecondRunSkipsUnchangedFiles(ctx context.Context, t *testing.T, adapter *gate.CoverageAdapter) {
	t.Helper()

	stats, err := adapter.Refresh(ctx)
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

	stats, err := adapter.Refresh(ctx)
	if err != nil {
		t.Fatalf("third Refresh: %v", err)
	}
	if stats.Ran != 2 || stats.Skipped != 0 {
		t.Fatalf("third Refresh stats = %+v, want Ran=2 Skipped=0 (greet.go's content hash changed)", stats)
	}
}
