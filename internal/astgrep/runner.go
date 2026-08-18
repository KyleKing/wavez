package astgrep

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// InstallHint is shown to a caller when neither ast-grep binary name is on
// PATH.
const InstallHint = "ast-grep not found on PATH; install with `brew install ast-grep` " +
	"or `cargo install ast-grep --locked` (https://ast-grep.github.io/guide/quick-start.html)"

// primaryBinary and fallbackBinary are ast-grep's two published binary
// names, per DESIGN.md's Structural rules section.
const (
	primaryBinary  = "ast-grep"
	fallbackBinary = "sg"
)

// ErrUnavailable is returned by Scan when neither ast-grep binary name
// resolves on PATH. A gate must treat this as a failed check, never as a
// pass: a check that cannot run is not a check that passed.
var ErrUnavailable = errors.New("astgrep: ast-grep binary not found on PATH")

// LookPathFunc resolves a binary name to a path, matching exec.LookPath's
// signature so tests can substitute a fake that reports every name absent.
type LookPathFunc func(file string) (string, error)

// CommandFunc runs one command and returns its stdout, matching what Scan
// needs from exec.Command so tests can substitute a fake ast-grep process.
type CommandFunc func(ctx context.Context, name string, args []string, dir string) ([]byte, error)

// Runner shells out to ast-grep to scan a repository against RuleFiles.
type Runner struct {
	lookPath LookPathFunc
	runCmd   CommandFunc
}

// Option configures a Runner.
type Option func(*Runner)

// WithLookPath overrides how Runner resolves the ast-grep binary, for
// tests that simulate the binary being absent.
func WithLookPath(fn LookPathFunc) Option {
	return func(r *Runner) { r.lookPath = fn }
}

// WithCommand overrides how Runner invokes ast-grep, for tests that drive
// a fake process instead of a real binary.
func WithCommand(fn CommandFunc) Option {
	return func(r *Runner) { r.runCmd = fn }
}

// NewRunner builds a Runner that resolves ast-grep via exec.LookPath and
// runs it via os/exec, unless overridden by Option.
func NewRunner(opts ...Option) *Runner {
	r := &Runner{lookPath: exec.LookPath, runCmd: runCommand}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Availability reports whether ast-grep can be invoked at all.
type Availability struct {
	Binary      string
	InstallHint string
	Available   bool
}

// Resolve checks whether either ast-grep binary name is on PATH, without
// running anything.
func (r *Runner) Resolve() Availability {
	for _, name := range []string{primaryBinary, fallbackBinary} {
		if path, err := r.lookPath(name); err == nil {
			return Availability{Available: true, Binary: path}
		}
	}

	return Availability{Available: false, InstallHint: InstallHint}
}

// Report is one Scan's outcome across every rule file it was given.
type Report struct {
	InstallHint string
	Findings    []Finding
	Available   bool
}

// Scan runs every rule file in ruleFiles against targets, or against
// repoRoot when targets is empty, and aggregates their findings. Passing
// the changed files as targets is what makes this cheap enough to gate on
// every edit. When ast-grep is not on PATH, Scan returns a Report with
// Available false and a non-nil error, since a check that cannot run must
// never be reported as a pass.
func (r *Runner) Scan(ctx context.Context, repoRoot string, ruleFiles []RuleFile, targets ...string) (Report, error) {
	avail := r.Resolve()
	if !avail.Available {
		return Report{Available: false, InstallHint: avail.InstallHint},
			fmt.Errorf("%w: %s", ErrUnavailable, avail.InstallHint)
	}

	scanIn := targets
	if len(scanIn) == 0 {
		scanIn = []string{repoRoot}
	}

	var findings []Finding

	for _, rf := range ruleFiles {
		args := append([]string{"scan", "--rule", rf.Path, "--json=compact"}, scanIn...)

		out, err := r.runCmd(ctx, avail.Binary, args, repoRoot)
		if err != nil {
			return Report{Available: true}, fmt.Errorf("ast-grep scan %s: %w", rf.ID, err)
		}

		parsed, err := ParseJSON(out)
		if err != nil {
			return Report{Available: true}, fmt.Errorf("ast-grep scan %s: %w", rf.ID, err)
		}

		findings = append(findings, parsed...)
	}

	return Report{Available: true, Findings: findings}, nil
}

// Pattern runs one bare structural pattern against targets, or against
// repoRoot when targets is empty, and returns what it matched. It is the
// sweep a Cycle's generalize phase triages: a proved cause expressed as one
// node shape, enumerated by the harness rather than recalled by the model.
//
// Findings carry no rule id, since a pattern is not a rule file.
func (r *Runner) Pattern(
	ctx context.Context, repoRoot, pattern, language string, targets ...string,
) (Report, error) {
	avail := r.Resolve()
	if !avail.Available {
		return Report{Available: false, InstallHint: avail.InstallHint},
			fmt.Errorf("%w: %s", ErrUnavailable, avail.InstallHint)
	}

	scanIn := targets
	if len(scanIn) == 0 {
		scanIn = []string{repoRoot}
	}

	args := append([]string{"run", "--pattern", pattern, "--lang", language, "--json=compact"}, scanIn...)

	out, err := r.runCmd(ctx, avail.Binary, args, repoRoot)
	if err != nil {
		return Report{Available: true}, fmt.Errorf("ast-grep run %q: %w", pattern, err)
	}

	findings, err := ParseJSON(out)
	if err != nil {
		return Report{Available: true}, fmt.Errorf("ast-grep run %q: %w", pattern, err)
	}

	return Report{Available: true, Findings: findings}, nil
}

// runCommand is CommandFunc's default implementation. Ast-grep exits 1 when
// a rule matches (DESIGN.md's Structural rules section calls this out as
// its own convention, not an error), so only a non-ExitError failure is
// treated as this call failing.
func runCommand(ctx context.Context, name string, args []string, dir string) ([]byte, error) {
	//nolint:gosec // name/args are this package's own resolved binary and flags
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	out, err := cmd.Output()

	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, fmt.Errorf("running %s: %w", name, err)
	}

	return out, nil
}
