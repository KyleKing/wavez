package gate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestFormatGateFixesMissingImport(t *testing.T) {
	t.Parallel()

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

// The format pre-pass is what turns the dogfooded "model forgot the import"
// failure into a compiling file.
func TestFormatGateAddsAMissingImport(t *testing.T) {
	t.Parallel()
	repoRoot := t.TempDir()
	goMod := filepath.Join(repoRoot, "go.mod")
	if err := os.WriteFile(goMod, []byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	src := "package a\n\nfunc F() string { return filepath.Join(\"a\", \"b\") }\n"
	if err := os.WriteFile(filepath.Join(repoRoot, "a.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	g := gate.NewFormatGate(repoRoot)
	rc := gate.RunContext{RepoRoot: repoRoot, Changes: []tool.Change{{Path: "a.go"}}}

	res, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run with an empty PATH: %v", err)
	}
	if !res.Pass {
		t.Fatalf("Result.Pass = false, want true")
	}

	out, err := os.ReadFile(filepath.Join(repoRoot, "a.go")) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("reading formatted file: %v", err)
	}
	if !strings.Contains(string(out), `"path/filepath"`) {
		t.Fatalf("missing import was not added:\n%s", out)
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

// imports.Process adds nothing when it cannot reach the go toolchain, which
// would read as a clean format rather than a check that never ran.
func TestFormatGateReportsAnAbsentGoToolchain(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	g := gate.NewFormatGate(repoRoot)
	rc := gate.RunContext{RepoRoot: repoRoot, Changes: []tool.Change{{Path: "a.go"}}}

	_, err := g.Run(context.Background(), rc)
	if err == nil {
		t.Fatal("Run: want an error when the go toolchain is absent")
	}
	if !strings.Contains(err.Error(), "go not found on PATH") {
		t.Fatalf("err = %v, want it to name the missing go binary", err)
	}
}

// The file list arrives from tool results, so containment is verified here
// rather than trusted.
func TestFormatGateRefusesAPathOutsideTheRepo(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	g := gate.NewFormatGate(repoRoot)
	rc := gate.RunContext{RepoRoot: repoRoot, Changes: []tool.Change{{Path: "../escape.go"}}}

	if _, err := g.Run(context.Background(), rc); !errors.Is(err, gate.ErrOutsideRepo) {
		t.Fatalf("err = %v, want ErrOutsideRepo", err)
	}
}
