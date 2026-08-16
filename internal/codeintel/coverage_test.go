package codeintel_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
)

func TestCoverage_QueryReturnsOverlappingTests(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	err := store.WriteCoverage(ctx, "TestA", "hashA", []codeintel.CoverageRow{
		{File: "pkgone/greeter.go", Start: 5, End: 12},
	})
	if err != nil {
		t.Fatalf("WriteCoverage TestA: %v", err)
	}
	err = store.WriteCoverage(ctx, "TestB", "hashB", []codeintel.CoverageRow{
		{File: "pkgone/greeter.go", Start: 1, End: 3},
	})
	if err != nil {
		t.Fatalf("WriteCoverage TestB: %v", err)
	}

	tests, err := store.CoveringTests(ctx, "pkgone/greeter.go", 8, 8)
	if err != nil {
		t.Fatalf("CoveringTests: %v", err)
	}
	if len(tests) != 1 || tests[0].TestID != "TestA" {
		t.Errorf("CoveringTests(line 8) = %+v, want only TestA", tests)
	}

	tests, err = store.CoveringTests(ctx, "pkgone/greeter.go", 1, 1)
	if err != nil {
		t.Fatalf("CoveringTests: %v", err)
	}
	if len(tests) != 1 || tests[0].TestID != "TestB" {
		t.Errorf("CoveringTests(line 1) = %+v, want only TestB", tests)
	}
}

func TestCoverage_WriteIsNoOpWhenHashUnchanged(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)

	original := []codeintel.CoverageRow{{File: "pkgone/greeter.go", Start: 5, End: 12}}
	if err := store.WriteCoverage(ctx, "TestA", "hashA", original); err != nil {
		t.Fatalf("first WriteCoverage: %v", err)
	}

	// Same hash, different rows: the store trusts the hash and must not
	// apply this content.
	changed := []codeintel.CoverageRow{{File: "pkgone/greeter.go", Start: 100, End: 200}}
	if err := store.WriteCoverage(ctx, "TestA", "hashA", changed); err != nil {
		t.Fatalf("second WriteCoverage: %v", err)
	}

	tests, err := store.CoveringTests(ctx, "pkgone/greeter.go", 8, 8)
	if err != nil {
		t.Fatalf("CoveringTests: %v", err)
	}
	if len(tests) != 1 || tests[0].TestID != "TestA" {
		t.Errorf("expected the original coverage row to survive, got %+v", tests)
	}

	stale, err := store.CoveringTests(ctx, "pkgone/greeter.go", 150, 150)
	if err != nil {
		t.Fatalf("CoveringTests: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected the same-hash write to be ignored, got %+v", stale)
	}
}
