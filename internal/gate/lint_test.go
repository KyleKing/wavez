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

// The linter's findings had nowhere to go: FormatGate runs it with --fix
// and discards the exit status, so anything it cannot fix reached nobody
// until CI. The prompt carried the rules instead, which is the expensive
// way to answer a question a tool answers for nothing.
//
//nolint:paralleltest // golangci-lint takes a machine-wide lock; two of these at once abort each other
func TestLintGateReportsWhatTheFixPassCannotFix(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	// A naked return is unfixable by --fix and is one of the rules the
	// project's Go conventions spell out in prose.
	root := lintFixture(t, "package a\n\nfunc F() (n int) {\n\tn = 1\n\treturn\n}\n")

	result := runLintGate(t, root)
	if result.Pass {
		t.Fatalf("Pass = true, want the finding: %+v", result)
	}

	if len(result.Failures) != 1 || len(result.Failures[0].Frames) == 0 {
		t.Fatalf("Failures = %+v, want one entry naming the findings", result.Failures)
	}

	if !strings.Contains(strings.Join(result.Failures[0].Frames, "\n"), "a.go") {
		t.Errorf("frames = %v, want them to name the changed file", result.Failures[0].Frames)
	}
}

// A gate that fails a run for the project's own setup rather than for the
// run's code is worse than one that says nothing, so both "no Go files" and
// "no linter" abstain.
func TestLintGateAbstainsRatherThanBlaming(t *testing.T) {
	t.Parallel()

	root := lintFixture(t, "package a\n")

	g := gate.NewLintGate(root)

	result, err := g.Run(context.Background(), gate.RunContext{
		RepoRoot: root, Changes: []tool.Change{{Path: "README.md"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.Pass || result.Examined != 0 || result.Reason == "" {
		t.Errorf("result = %+v, want an abstention naming its reason", result)
	}
}

func lintFixture(t *testing.T, source string) string {
	t.Helper()

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module fixture\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	// Without a config the linter runs its own defaults, which enable none
	// of the rules the project's conventions spell out.
	config := "version = \"2\"\n\n[linters]\nenable = [\"nakedret\"]\n\n" +
		"[linters.settings.nakedret]\nmax-func-lines = 1\n"
	if err := os.WriteFile(filepath.Join(root, ".golangci.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("writing linter config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	return root
}

func runLintGate(t *testing.T, root string) gate.Result {
	t.Helper()

	result, err := gate.NewLintGate(root).Run(context.Background(), gate.RunContext{
		RepoRoot: root, Changes: []tool.Change{{Path: "a.go"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return result
}

// A package that will not compile makes the linter report every type error
// as its own finding, and BuildGate reports the same errors in the same
// round. 168 of the 264 lint findings logged against a model were that
// duplicate, so this gate now says nothing and lets the build gate speak.
//
//nolint:paralleltest // same lock as the test above
func TestLintGateLeavesCompileErrorsToTheBuildGate(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	root := lintFixture(t, "package a\n\nfunc F() int {\n\treturn undefinedSymbol\n}\n")

	result := runLintGate(t, root)
	if len(result.Failures) != 0 {
		t.Fatalf("Failures = %+v, want none: the build gate reports a compile error", result.Failures)
	}
	if result.Reason == "" {
		t.Errorf("Reason = %q, want it to name the build gate", result.Reason)
	}
}
