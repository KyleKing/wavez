package gate_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

func TestBuildGateCompiles(t *testing.T) {
	t.Parallel()

	repoRoot := buildFixture(t, "package a\n\nfunc F() int { return 1 }\n")

	result := runBuildGate(t, repoRoot)
	if !result.Pass {
		t.Fatalf("Pass = false, want true: %+v", result)
	}
}

func TestBuildGateMissingImportTrimsToChangedFile(t *testing.T) {
	t.Parallel()

	source := "package a\n\nfunc Dir(p string) string {\n\treturn filepath.Dir(p)\n}\n"
	repoRoot := buildFixture(t, source)

	result := runBuildGate(t, repoRoot)
	if result.Pass {
		t.Fatalf("Pass = true, want false: %+v", result)
	}
	if len(result.Failures) != 1 || len(result.Failures[0].Frames) == 0 {
		t.Fatalf("Failures = %+v, want one entry with trimmed frames", result.Failures)
	}
	if !strings.Contains(result.Failures[0].Frames[0], "a.go") {
		t.Errorf("frame = %q, want it to reference a.go", result.Failures[0].Frames[0])
	}
}

// Writing the binaries rather than discarding them is what keeps the link
// out of every run, and `-o` is an error on a module with no main package.
// Both shapes have to pass, and the library-only one is the default fixture
// every other case here uses.
func TestBuildGateCompilesAModuleWithAMainPackage(t *testing.T) {
	t.Parallel()

	repoRoot := buildFixture(t, "package main\n\nfunc main() { _ = 1 }\n")

	result := runBuildGate(t, repoRoot)
	if !result.Pass {
		t.Fatalf("Pass = false, want true: %+v", result)
	}
}

func buildFixture(t *testing.T, source string) string {
	t.Helper()

	repoRoot := t.TempDir()

	goMod := filepath.Join(repoRoot, "go.mod")
	if err := os.WriteFile(goMod, []byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "a.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	return repoRoot
}

func runBuildGate(t *testing.T, repoRoot string) gate.Result {
	t.Helper()

	g := gate.NewBuildGate(repoRoot)
	rc := gate.RunContext{RepoRoot: repoRoot, Changes: []tool.Change{{Path: "a.go"}}}

	result, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return result
}

// A project that is not a Go module is not a broken build. Pointed at a
// Python repository this gate reported the Go toolchain's complaint that the
// directory contains no main module as a failure, and the run answered that
// error rather than the task it was given.
func TestBuildGateAbstainsOutsideAGoModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("def f():\n    return 1\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	result := runBuildGate(t, root)
	if !result.Pass || result.Examined != 0 {
		t.Fatalf("Pass = %v, Examined = %d, want an abstention: %+v", result.Pass, result.Examined, result)
	}

	if len(result.Failures) > 0 {
		t.Errorf("Failures = %+v, want none", result.Failures)
	}

	if !strings.Contains(result.Reason, "go.mod") {
		t.Errorf("Reason = %q, want it to name go.mod", result.Reason)
	}
}
