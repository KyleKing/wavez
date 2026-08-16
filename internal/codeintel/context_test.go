package codeintel_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
)

func TestContext_FindsTouchedSymbolAndCoveringTests(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}
	err := store.WriteCoverage(ctx, "TestGreet", "hash1", []codeintel.CoverageRow{
		{File: "pkgone/greeter.go", Start: 10, End: 12},
	})
	if err != nil {
		t.Fatalf("WriteCoverage: %v", err)
	}

	touched := []codeintel.TouchedRange{{File: "pkgone/greeter.go", Start: 11, End: 11}}
	bundle, err := store.Context(ctx, codeintel.ContextRequest{Touched: touched})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if len(bundle.Symbols) != 1 || bundle.Symbols[0].Name != "Greet" {
		t.Fatalf("Symbols = %+v, want exactly the Greet method", bundle.Symbols)
	}
	if len(bundle.Tests) != 1 || bundle.Tests[0].TestID != "TestGreet" {
		t.Errorf("Tests = %+v, want exactly TestGreet", bundle.Tests)
	}
	if bundle.Truncated {
		t.Error("Truncated = true with no budget set, want false")
	}
}

func TestContext_RespectsTokenBudget(t *testing.T) {
	t.Parallel()
	store, ctx := openStore(t)
	if _, err := store.Index(ctx, fixtureDir, defaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	touched := []codeintel.TouchedRange{{File: "pkgone/greeter.go", Start: 11, End: 11}}

	tight, err := store.Context(ctx, codeintel.ContextRequest{Touched: touched, TokenBudget: 1})
	if err != nil {
		t.Fatalf("Context (tight budget): %v", err)
	}
	if !tight.Truncated || len(tight.Symbols) != 0 {
		t.Errorf("tight budget bundle = %+v, want Truncated with no symbols", tight)
	}

	roomy, err := store.Context(ctx, codeintel.ContextRequest{Touched: touched, TokenBudget: 1000})
	if err != nil {
		t.Fatalf("Context (roomy budget): %v", err)
	}
	if roomy.Truncated || len(roomy.Symbols) != 1 {
		t.Errorf("roomy budget bundle = %+v, want the Greet symbol and no truncation", roomy)
	}
	if roomy.TokensUsed <= 0 {
		t.Error("expected TokensUsed to reflect the included symbol")
	}
}
