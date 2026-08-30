package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linkCacheDirPerm is the mode linkCache creates its directories with.
const linkCacheDirPerm = 0o700

// goBuild is the go subcommand this gate runs.
const goBuild = "build"

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

	args, err := g.buildArgs(ctx)
	if err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
	}

	//nolint:gosec // args are this gate's own literals plus a path under the user cache directory
	cmd := exec.CommandContext(ctx, "go", args...)
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

// buildArgs is the `go build` invocation for this module. It writes the
// binaries somewhere rather than discarding them, because discarding them
// leaves nothing to reuse and so pays the link on every run: on this project
// a fully cached build is 3.0s discarded and 0.45s written, with identical
// compiler output. `-o` is an error on a module with no main package, which
// is why the main packages are listed first, and a listing that comes back
// empty because the tree does not parse falls to the plain form, where the
// same failure is reported as the compile error it is.
func (g *BuildGate) buildArgs(ctx context.Context) ([]string, error) {
	if !g.hasMainPackage(ctx) {
		return []string{goBuild, wholeModule}, nil
	}

	outDir, err := g.linkCache()
	if err != nil {
		return nil, err
	}

	return []string{goBuild, "-o", outDir + string(os.PathSeparator), wholeModule}, nil
}

// hasMainPackage reports whether the module holds a package `go build -o`
// would link. It costs 0.3s against the 2.6s the link costs when discarded.
func (g *BuildGate) hasMainPackage(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "go", "list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, wholeModule)
	cmd.Dir = g.repoRoot

	out, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(out)) != ""
}

// linkCache is where this gate writes the binaries `go build` produces, one
// directory per project under the user cache, so a repository Wavez did not
// write never gains an untracked directory it would then have to ignore.
func (g *BuildGate) linkCache() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating the user cache directory: %w", err)
	}

	sum := sha256.Sum256([]byte(g.repoRoot))
	dir := filepath.Join(base, "wavez", "build", hex.EncodeToString(sum[:8]))

	if err := os.MkdirAll(dir, linkCacheDirPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	return dir, nil
}
