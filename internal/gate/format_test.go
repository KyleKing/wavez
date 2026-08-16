package gate_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// requireGoimports skips when goimports is absent, since the format pre-pass
// shells out to it and the pin lives in mise rather than in the module.
func requireGoimports(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports not on PATH; run under `mise exec --`")
	}
}

func TestFormatGateRewritesUnformattedFiles(t *testing.T) {
	t.Parallel()
	requireGoimports(t)

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

func TestFormatGateFixesMissingImport(t *testing.T) {
	t.Parallel()
	requireGoimports(t)

	repoRoot := t.TempDir()

	goMod := filepath.Join(repoRoot, "go.mod")
	if err := os.WriteFile(goMod, []byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	path := filepath.Join(repoRoot, "a.go")
	// The missing path/filepath import DESIGN.md's dogfood run measured as
	// a residual failure the format pre-pass, not the model, must resolve.
	missingImport := "package a\n\nfunc Dir(p string) string {\n\treturn filepath.Dir(p)\n}\n"
	if err := os.WriteFile(path, []byte(missingImport), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	g := gate.NewFormatGate(repoRoot)
	rc := gate.RunContext{RepoRoot: repoRoot, Changes: []tool.Change{{Path: "a.go"}}}

	result, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true: %+v", result)
	}

	got, err := os.ReadFile(path) //nolint:gosec // path is this test's own temp fixture file
	if err != nil {
		t.Fatalf("reading fixed file: %v", err)
	}
	if !strings.Contains(string(got), `"path/filepath"`) {
		t.Errorf("goimports output = %q, want it to add the path/filepath import", got)
	}
}

func TestFormatGateReportsMissingGoimports(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	gofmtPath, err := exec.LookPath("gofmt")
	if err != nil {
		t.Fatalf("LookPath(gofmt): %v", err)
	}

	binDir := t.TempDir()
	if err := os.Symlink(gofmtPath, filepath.Join(binDir, "gofmt")); err != nil {
		t.Fatalf("symlinking gofmt: %v", err)
	}
	t.Setenv("PATH", binDir)

	g := gate.NewFormatGate(repoRoot)
	rc := gate.RunContext{RepoRoot: repoRoot, Changes: []tool.Change{{Path: "a.go"}}}

	if _, err := g.Run(context.Background(), rc); err == nil {
		t.Fatal("Run: want an error when goimports is absent from PATH")
	} else if !strings.Contains(err.Error(), "goimports") {
		t.Errorf("err = %v, want it to name goimports", err)
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
