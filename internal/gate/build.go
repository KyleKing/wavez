package gate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// BuildGate runs `go build ./...` over the whole module: a compile failure
// anywhere blocks every package regardless of which files changed, so
// selection never narrows what this gate builds.
type BuildGate struct {
	repoRoot string
}

// NewBuildGate builds a BuildGate rooted at repoRoot.
func NewBuildGate(repoRoot string) *BuildGate {
	return &BuildGate{repoRoot: repoRoot}
}

// Name identifies this gate in the gate log.
func (*BuildGate) Name() string { return "build" }

// Resources reports the exclusive resource this gate holds while running:
// it shares the Go toolchain's build cache with GoTestGate.
func (*BuildGate) Resources() []string { return []string{goTestResource} }

// Run compiles the module and, on failure, trims the compiler output to
// the lines that reference a changed file. It returns a non-nil error only
// when `go build` itself could not run (the binary is missing, or ctx was
// canceled); a compile failure is reported through Result instead, since
// that is the outcome a Verifier feeds back to the model.
func (g *BuildGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = g.repoRoot

	// The unit here is the module: this gate never narrows, so it examines
	// exactly one thing every run and can never abstain.
	out, err := cmd.CombinedOutput()
	if err == nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: 1, Pass: true}, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, fmt.Errorf("go build ./...: %w", err)
	}

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	failure := TrimFailure(FailedTest{Name: "build", Output: lines}, changedPaths(rc.Changes))

	return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: 1, Failures: []TrimmedFailure{failure}}, nil
}
