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

	return runLintGateAs(t, root, "")
}

// runLintGateAs runs the gate under runID, the identity a run hands its
// batches. The same gate value is reused so its baseline survives the call,
// the way a project's lint gate survives a run's batches.
func runLintGateAs(t *testing.T, root, runID string) gate.Result {
	t.Helper()

	result, err := gate.NewLintGate(root).Run(context.Background(), gate.RunContext{
		RepoRoot: root, Changes: []tool.Change{{Path: "a.go"}}, RunID: runID,
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

// The gate lints whole packages and used to keep only the findings naming a
// changed file, so a finding on a neighbor was linted and then dropped: the
// run passed every round and CI reported it. It is an advisory now, which
// puts it in the gate log without blaming the run for what it may have
// inherited.
//
//nolint:paralleltest // same lock as the tests above
func TestLintGateRecordsANeighborsFindingRatherThanDroppingIt(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	root := lintFixtureWithSibling(t,
		"package a\n\nfunc F() int { return G() }\n",
		"package a\n\nfunc G() (n int) {\n\tn = 1\n\treturn\n}\n")

	result := runLintGate(t, root)
	if result.Reason != "" {
		t.Fatalf("gate abstained on a package that compiles: %+v", result)
	}

	if !result.Pass || len(result.Failures) != 0 {
		t.Fatalf("result = %+v, want the run's own files to pass", result)
	}

	if len(result.Advisories) != 1 {
		t.Fatalf("advisories = %+v, want the neighbor's naked return recorded", result.Advisories)
	}

	if frames := result.Advisories[0].Frames; len(frames) != 1 || !strings.Contains(frames[0], "b.go") {
		t.Errorf("advisory = %q, want it to name b.go", frames)
	}
}

// A finding the package already carried when the run began is one the run
// inherited: the first lint under the run's identity records the baseline,
// and on every later round that finding stays an advisory, so the gate log
// holds it without the run being blamed for a neighbor's old work.
//
//nolint:paralleltest // same lock as the tests above
func TestLintGateKeepsABaselineFindingAdvisory(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	root := lintFixtureWithSibling(t,
		"package a\n\nfunc F() int { return G() }\n",
		"package a\n\nfunc G() (n int) {\n\tn = 1\n\treturn\n}\n")

	g := gate.NewLintGate(root)
	rc := gate.RunContext{RepoRoot: root, Changes: []tool.Change{{Path: "a.go"}}, RunID: "run-baseline"}

	for range 2 {
		result, err := g.Run(context.Background(), rc)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if !result.Pass || len(result.Failures) != 0 {
			t.Fatalf("result = %+v, want the inherited finding not to fail the run", result)
		}

		if len(result.Advisories) != 1 || !strings.Contains(result.Advisories[0].Frames[0], "b.go") {
			t.Fatalf("advisories = %+v, want b.go's finding recorded as inherited", result.Advisories)
		}
	}
}

// A finding the baseline did not hold is the run's own, whatever file it
// names. The first lint under a run's identity sees b.go clean, the run's
// edit breaks it, and the second lint hands the finding to the run as a
// failure instead of an advisory: this is the work the gate used to lint
// and then discard.
//
//nolint:paralleltest // same lock as the tests above
func TestLintGateHandsANewNeighborFindingToTheRun(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	// The neighbor starts clean and the package compiles throughout: a
	// baseline is only recorded by a lint that ran, and a package that does
	// not build abstains without recording one.
	root := lintFixtureWithSibling(t,
		"package a\n\nfunc F() int { return G() }\n",
		"package a\n\nfunc G() int {\n\tn := 1\n\n\treturn n\n}\n")

	g := gate.NewLintGate(root)
	rc := gate.RunContext{RepoRoot: root, Changes: []tool.Change{{Path: "a.go"}}, RunID: "run-fresh"}

	first, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !first.Pass || len(first.Advisories) != 0 {
		t.Fatalf("first result = %+v, want a clean starting point", first)
	}

	// The run's work leaves the neighbor it never wrote with a finding,
	// which is exactly what the gate used to discard.
	naked := "package a\n\nfunc G() (n int) {\n\tn = 1\n\n\treturn\n}\n"
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte(naked), 0o600); err != nil {
		t.Fatalf("rewriting b.go: %v", err)
	}

	second, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if second.Pass || len(second.Failures) != 1 || !strings.Contains(second.Failures[0].Frames[0], "b.go") {
		t.Fatalf("result = %+v, want the new neighbor finding to reach the run", second)
	}
}

// With no run identity the gate has no baseline to read and behaves exactly
// as it did before one existed: every neighbor's finding is an advisory,
// including one that is new since the last lint.
//
//nolint:paralleltest // same lock as the tests above
func TestLintGateWithoutARunBehavesAsBefore(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint is not installed")
	}

	root := lintFixtureWithSibling(t,
		"package a\n\nfunc F() int { return G() }\n",
		"package a\n\nfunc G() (n int) {\n\tn = 1\n\treturn\n}\n")

	g := gate.NewLintGate(root)
	rc := gate.RunContext{RepoRoot: root, Changes: []tool.Change{{Path: "a.go"}}}

	for range 2 {
		result, err := g.Run(context.Background(), rc)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if !result.Pass || len(result.Failures) != 0 {
			t.Fatalf("result = %+v, want no failure with no run identity", result)
		}

		if len(result.Advisories) != 1 {
			t.Fatalf("advisories = %+v, want the neighbor's finding recorded", result.Advisories)
		}
	}
}
