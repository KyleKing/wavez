package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// goModule reports whether repoRoot is the root of a Go module.
func goModule(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, "go.mod"))

	return err == nil
}

// BuildGate runs `go build ./...` over the whole module: a compile failure
// anywhere blocks every package regardless of which files changed, so
// selection never narrows what this gate builds.
//
// It is the only Go gate that does not scope itself to the changed Go files,
// which is why it is the only one that has to ask whether the project is a Go
// module at all. Pointed at a Python repository it reported the Go
// toolchain's complaint that the directory contains no main module as a gate
// failure, and the run spent its remaining turns explaining that error
// instead of doing the task.
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
	if !goModule(g.repoRoot) {
		return Abstained(g.Name(), rc.Selection.Level, "the project root holds no go.mod"), nil
	}

	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = g.repoRoot

	// The unit here is the module: this gate never narrows, so it examines
	// exactly one thing on every run of a Go project.
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
