package gate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// lintTool is the linter this gate runs. It is the same binary FormatGate
// invokes with --fix, and this gate is what reads the findings that pass
// could not fix.
const lintTool = "golangci-lint"

// lintGateName identifies this gate in the log and names its one failure.
const lintGateName = "lint"

// maxLintFindings bounds what one gate run hands back. A change that breaks
// a hundred rules is one mistake, and listing all of them buries it.
const maxLintFindings = 10

// typecheckSuffix marks a compile error rather than a rule. One of these
// anywhere in a linted package means most linters did not run, so the gate
// abstains and lets BuildGate report the error once.
const typecheckSuffix = "(typecheck)"

// LintGate reports the linter findings on a run's own changed files.
//
// It exists because the findings had nowhere to go. FormatGate runs
// `golangci-lint run --fix` and discards the exit status, so an autofixable
// finding is silently corrected and every other one reaches nobody until
// CI, long after the run that wrote it ended. The prompt carried the rules
// instead, which is the expensive way to answer a question a tool can
// answer for nothing.
type LintGate struct {
	repoRoot string
}

// NewLintGate builds a LintGate rooted at repoRoot.
func NewLintGate(repoRoot string) *LintGate { return &LintGate{repoRoot: repoRoot} }

// Name identifies this gate in the gate log.
func (*LintGate) Name() string { return lintGateName }

// Resources reports the exclusive resource this gate holds while running.
// It type-checks the packages it lints, so it shares the Go build cache
// with the gates that compile.
func (*LintGate) Resources() []string { return []string{goTestResource} }

// Run lints the Go files this batch changed. It returns a non-nil error
// only when the linter could not run at all; a finding is reported through
// Result, which is what reaches the model.
//
// A project with no linter installed abstains rather than failing: the
// linter is the project's choice and its absence is not the run's defect.
func (g *LintGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	files, err := presentGoFiles(g.repoRoot, rc.Changes)
	if err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, err
	}
	if len(files) == 0 {
		return Abstained(g.Name(), rc.Selection.Level, "the change set holds no Go files"), nil
	}

	path, lookErr := exec.LookPath(lintTool)
	if lookErr != nil {
		//nolint:nilerr // a project without the linter is not a run that failed a check
		return Abstained(g.Name(), rc.Selection.Level, lintTool+" is not installed"), nil
	}

	//nolint:gosec // the arguments are this gate's own changed-file list
	cmd := exec.CommandContext(ctx, path, slices.Concat(lintArgs, packagesOf(files))...)
	cmd.Dir = g.repoRoot

	out, err := cmd.CombinedOutput()
	if err == nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: len(files), Pass: true}, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Result{Gate: g.Name(), Level: rc.Selection.Level},
			fmt.Errorf("%s run: %w: %s", lintTool, err, strings.TrimSpace(string(out)))
	}

	failures := lintFailures(out, rc.Changes)
	if len(failures) == 0 {
		return Abstained(g.Name(), rc.Selection.Level,
			"the change does not compile, which the build gate reports"), nil
	}

	return Result{
		Gate: g.Name(), Level: rc.Selection.Level, Examined: len(files),
		Failures: failures,
	}, nil
}

// lintArgs run the linter the way CI does: golangci-lint's default caps
// hide findings CI then reports, and maxLintFindings is this gate's bound.
var lintArgs = []string{"run", "--max-issues-per-linter=0", "--max-same-issues=0"}

// packagesOf names the directories a change set's files sit in, since the
// linter type-checks whatever it is handed: given a file it sees one file
// of a package and reports every symbol declared elsewhere in that package
// as undefined, which this gate then reads as a compile error and abstains
// on. Findings are narrowed back to the changed files afterwards.
func packagesOf(files []string) []string {
	seen := map[string]bool{}

	var out []string

	for _, f := range files {
		dir := "./" + filepath.ToSlash(filepath.Dir(f))
		if seen[dir] {
			continue
		}

		seen[dir] = true

		out = append(out, dir)
	}

	return out
}

// lintFailures turns the linter's output into one failure per finding, so a
// model reads which rule it broke and where rather than a wall of report.
// A finding always names its own file and line, so unlike a compiler or a
// test there is nothing to trim: every line is already about the change.
func lintFailures(out []byte, changes []tool.Change) []TrimmedFailure {
	changed := changedPaths(changes)
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")

	// One type error anywhere in a linted package stops most linters from
	// running at all, so whatever else the output holds is not a reading of
	// the change. The build gate reports the error in the same round.
	for _, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), typecheckSuffix) {
			return nil
		}
	}

	var findings []string

	for _, line := range lines {
		if namesAChangedFile(line, changed) {
			findings = append(findings, strings.TrimSpace(line))
		}
	}

	if len(findings) == 0 {
		return []TrimmedFailure{{Test: lintGateName, Context: headOf(lines)}}
	}

	dropped := 0
	if len(findings) > maxLintFindings {
		dropped = len(findings) - maxLintFindings
		findings = findings[:maxLintFindings]
	}

	if dropped > 0 {
		findings = append(findings, fmt.Sprintf("(%d more not shown)", dropped))
	}

	return []TrimmedFailure{{Test: lintGateName, Frames: findings}}
}

func namesAChangedFile(line string, changed []string) bool {
	for _, path := range changed {
		if strings.Contains(line, path) {
			return true
		}
	}

	return false
}

// headOf is the first few lines of output that named no changed file, which
// is how a linter reports its own failure (a bad config, a package that
// will not typecheck) rather than the run's.
func headOf(lines []string) []string {
	const head = 5

	if len(lines) > head {
		return lines[:head]
	}

	return lines
}
