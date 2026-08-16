package gate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// FormatGate runs gofmt, and golangci-lint --fix when the binary is on
// PATH, over changed Go files. DESIGN.md's Gates section puts this before
// the model ever sees a diff.
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
	files := goFiles(rc.Changes)
	if len(files) == 0 {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: true}, nil
	}

	if err := g.gofmt(ctx, files); err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
	}

	if lintPath, err := exec.LookPath("golangci-lint"); err == nil {
		if err := g.golangciFix(ctx, lintPath, files); err != nil {
			return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
		}
	}

	return Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: true}, nil
}

func (g *FormatGate) gofmt(ctx context.Context, files []string) error {
	//nolint:gosec // files are this gate's own changed-file list
	cmd := exec.CommandContext(ctx, "gofmt", append([]string{"-w"}, files...)...)
	cmd.Dir = g.repoRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gofmt -w: %w: %s", err, strings.TrimSpace(string(out)))
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

func goFiles(changes []tool.Change) []string {
	var out []string

	for _, c := range changes {
		if filepath.Ext(c.Path) == ".go" {
			out = append(out, c.Path)
		}
	}

	return out
}
