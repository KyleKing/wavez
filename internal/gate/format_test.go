package gate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

func TestFormatGateRewritesUnformattedFiles(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	path := filepath.Join(repoRoot, "a.go")

	unformatted := "package a\nfunc F(){return}\n"
	if err := os.WriteFile(path, []byte(unformatted), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	g := gate.NewFormatGate(repoRoot)
	rc := gate.RunContext{
		RepoRoot: repoRoot,
		Changes:  []tool.Change{{Path: "a.go"}},
	}

	result, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true: %+v", result)
	}

	got, err := os.ReadFile(path) //nolint:gosec // path is this test's own temp fixture file
	if err != nil {
		t.Fatalf("reading formatted file: %v", err)
	}

	want := "package a\n\nfunc F() { return }\n"
	if string(got) != want {
		t.Errorf("gofmt output = %q, want %q", got, want)
	}
}

func TestFormatGateNoOpWithoutGoFiles(t *testing.T) {
	t.Parallel()

	g := gate.NewFormatGate(t.TempDir())
	rc := gate.RunContext{Changes: []tool.Change{{Path: "README.md"}}}

	result, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Errorf("Pass = false, want true for a batch with no Go files")
	}
}
