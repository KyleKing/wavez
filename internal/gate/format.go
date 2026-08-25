package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/tools/imports"

	"github.com/kyleking/wavez/internal/tool"
)

// ErrOutsideRepo reports a changed-file path that resolves outside the repo.
var ErrOutsideRepo = errors.New("path escapes the repository root")

const (
	formattedFilePerm = 0o644
	gofmtTabWidth     = 8
)

// FormatGate inserts the t.Parallel() call a changed test is missing, then
// runs gofmt, goimports, and golangci-lint --fix when the binary is on
// PATH, over changed Go files. DESIGN.md's Gates section puts
// this before the model ever sees a diff: the missing import and the
// indentation failures in _ai_/bench/dogfood.md were both deterministic,
// so they belong here rather than in a retry against the model.
type FormatGate struct {
	repoRoot string
}

// NewFormatGate builds a FormatGate rooted at repoRoot.
func NewFormatGate(repoRoot string) *FormatGate {
	return &FormatGate{repoRoot: repoRoot}
}

// Name identifies this gate in the gate log.
func (*FormatGate) Name() string { return "format" }

// Resources reports the exclusive resource this gate holds while running:
// it rewrites files in place, so it must not overlap another gate doing the
// same.
func (*FormatGate) Resources() []string { return []string{"worktree"} }

// Run formats and fixes every changed Go file.
func (g *FormatGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	// No Go files is a real abstention, not a pass with work done: the
	// Examined count in the log is what keeps the two distinguishable.
	files, err := presentGoFiles(g.repoRoot, rc.Changes)
	if err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
	}
	if len(files) == 0 {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: true}, nil
	}

	// imports.Process resolves packages through the go toolchain and silently
	// adds nothing when it cannot find one, so an absent go binary would look
	// like a clean format rather than a check that never ran.
	if _, err := exec.LookPath("go"); err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level},
			fmt.Errorf("go not found on PATH, so imports cannot be resolved: %w", err)
	}

	if err := g.parallelizeTests(files); err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
	}

	if err := g.formatFiles(files); err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
	}

	if lintPath, err := exec.LookPath("golangci-lint"); err == nil {
		if err := g.golangciFix(ctx, lintPath, files); err != nil {
			return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
		}
	}

	return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: len(files), Pass: true}, nil
}

// parallelizeTests inserts the t.Parallel() call the project requires into
// every changed test that can take one, before gofmt runs over the splice.
func (g *FormatGate) parallelizeTests(files []string) error {
	root, err := filepath.Abs(g.repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}

	for _, f := range files {
		path, err := containedPath(root, f)
		if err != nil {
			return err
		}

		if err := parallelizeFile(path); err != nil {
			return err
		}
	}

	return nil
}

// containedPath resolves f against root and refuses anything that escapes it.
// The file list arrives from tool results, so the pre-pass verifies containment
// itself rather than trusting whoever produced it.
func containedPath(root, f string) (string, error) {
	path := f
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}

	path = filepath.Clean(path)
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRepo, f)
	}

	return path, nil
}

// formatFiles applies gofmt and goimports in process. Both are libraries, so
// the pre-pass has no PATH dependency and a released binary formats the same
// way a developer's checkout does.
func (g *FormatGate) formatFiles(files []string) error {
	opts := &imports.Options{Comments: true, TabIndent: true, TabWidth: gofmtTabWidth, FormatOnly: false}

	root, err := filepath.Abs(g.repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}

	for _, f := range files {
		path, err := containedPath(root, f)
		if err != nil {
			return err
		}

		//nolint:gosec // path comes from this gate's own changed-file list, never model input
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", f, err)
		}

		out, err := imports.Process(path, src, opts)
		if err != nil {
			// A file that does not parse is the build gate's report to make,
			// not a reason to fail formatting.
			continue
		}
		if bytes.Equal(out, src) {
			continue
		}
		//nolint:gosec // containedPath already refused anything outside repoRoot
		if err := os.WriteFile(path, out, formattedFilePerm); err != nil {
			return fmt.Errorf("writing %s: %w", f, err)
		}
	}

	return nil
}

// golangciFix runs golangci-lint's own autofixes and ignores its exit
// status: an unfixable finding is the lint gate's concern, not this
// pre-pass's.
func (g *FormatGate) golangciFix(ctx context.Context, lintPath string, files []string) error {
	//nolint:gosec // files are this gate's own changed-file list
	cmd := exec.CommandContext(ctx, lintPath, append([]string{"run", "--fix"}, files...)...)
	cmd.Dir = g.repoRoot

	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return fmt.Errorf("golangci-lint run --fix: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// presentGoFiles is the changed Go files still on disk. A deletion is a
// change and is not a file to read: one run deleted the test file its task
// asked it to delete, and the format gate then failed three rounds on the
// file being gone, which is feedback no edit can answer.
func presentGoFiles(repoRoot string, changes []tool.Change) ([]string, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving repo root: %w", err)
	}

	var out []string

	for _, f := range goFiles(changes) {
		path, err := containedPath(root, f)
		if err != nil {
			return nil, err
		}

		if _, err := os.Stat(path); err != nil {
			continue
		}

		out = append(out, f)
	}

	return out, nil
}

func goFiles(changes []tool.Change) []string {
	var out []string

	for _, c := range changes {
		if filepath.Ext(c.Path) == goSourceExt {
			out = append(out, c.Path)
		}
	}

	return out
}
