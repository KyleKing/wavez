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

	frames := strings.Join(result.Failures[0].Frames, "\n")
	if !strings.Contains(frames, "a.go") {
		t.Errorf("frames = %v, want them to name the changed file", result.Failures[0].Frames)
	}

	// Under a symlinked temp root the linter narrates its own trouble
	// resolving that path, quoting the whole result.Issue struct. Reading
	// those lines as findings costs a run a turn to explain away.
	if strings.Contains(frames, "level=warning") {
		t.Errorf("frames = %v, want the linter's own diagnostics left out", result.Failures[0].Frames)
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

	return lintFixtureWithSibling(t, source, "")
}

// lintFixtureWithSibling writes a package whose second file, when sibling is
// non-empty, holds a declaration a.go depends on.
func lintFixtureWithSibling(t *testing.T, source, sibling string) string {
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

	if sibling != "" {
		if err := os.WriteFile(filepath.Join(root, "b.go"), []byte(sibling), 0o600); err != nil {
			t.Fatalf("writing sibling file: %v", err)
		}
	}

	return root
}

// The linter type-checks whatever it is handed, so a changed file passed on
// its own is one file of its package and every symbol declared in a sibling
// reads as undefined. This gate took those for compile errors and abstained,
// which made it silent on almost every real change: a package of one file is
// the only shape that ever reported a finding.
//
//nolint:paralleltest // same lock as the tests above
func TestLintGateSeesPastASiblingFile(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	root := lintFixtureWithSibling(t,
		"package a\n\nfunc F() (n int) {\n\tn = G()\n\treturn\n}\n",
		"package a\n\nfunc G() int { return 1 }\n")

	result := runLintGate(t, root)
	if result.Reason != "" {
		t.Fatalf("gate abstained on a package that compiles: %+v", result)
	}

	if result.Pass || len(result.Failures) != 1 {
		t.Fatalf("result = %+v, want the naked return reported", result)
	}
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

// Two byte-identical checkouts are what a replay lane makes, and the
// linter's results cache is keyed by package content and holds absolute
// paths, so the second was answered with the first's file: findings under a
// directory already deleted, where no nolint directive could be honored.
//
//nolint:paralleltest // same lock as the tests above
func TestLintGateIsNotAnsweredWithASiblingCheckoutsPaths(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	source := "package a\n\nfunc F() (n int) {\n\tn = 1\n\treturn\n}\n"

	first := lintFixture(t, source)
	runLintGate(t, first)

	if err := os.RemoveAll(first); err != nil {
		t.Fatalf("removing the first checkout: %v", err)
	}

	result := runLintGate(t, lintFixture(t, source))
	if result.Pass || len(result.Failures) != 1 {
		t.Fatalf("result = %+v, want the naked return reported", result)
	}

	if frames := result.Failures[0].Frames; !strings.HasPrefix(frames[0], "a.go:") {
		t.Errorf("frames = %v, want the finding under the checkout that was linted", frames)
	}
}
