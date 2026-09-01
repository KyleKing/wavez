package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	cmd.Env = lintEnv(g.repoRoot)

	out, err := cmd.CombinedOutput()
	if err == nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: len(files), Pass: true}, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Result{Gate: g.Name(), Level: rc.Selection.Level},
			fmt.Errorf("%s run: %w: %s", lintTool, err, strings.TrimSpace(string(out)))
	}

	failures, advisories, sawFinding := lintFailures(out, rc.Changes)
	if !sawFinding && len(failures) == 0 {
		return Abstained(g.Name(), rc.Selection.Level,
			"the change does not compile, which the build gate reports"), nil
	}

	for i := range advisories {
		advisories[i].Writer = otherWriter
	}

	return Result{
		Gate: g.Name(), Level: rc.Selection.Level, Examined: len(files),
		Failures: failures, Advisories: advisories, Pass: len(failures) == 0,
	}, nil
}

// lintCache is the linter's results cache, under the state directory of the
// repository it lints.
//
// The linter keys that cache by package content and stores absolute paths
// in it, so two byte-identical checkouts share entries and the second
// is answered with the first's paths. A replay workspace over this project
// was handed findings under a sibling workspace already deleted, where a
// suppression directive could not be honored and --fix would have edited at
// a position from another tree.
const lintCache = ".wavez/cache/golangci-lint"

// lintEnv is the environment the linter runs under from repoRoot.
func lintEnv(repoRoot string) []string {
	return append(os.Environ(),
		"GOLANGCI_LINT_CACHE="+filepath.Join(repoRoot, filepath.FromSlash(lintCache)))
}

// lintArgs run the linter the way CI does: golangci-lint's default caps
// hide findings CI then reports, and maxLintFindings is this gate's bound.
//
// Its file lock is one per machine rather than one per cache, so without
// --allow-serial-runners a gate that starts while an editor or a CI step is
// linting exits at once with "parallel golangci-lint is running", which
// reaches the run as a gate failure about nothing it wrote.
var lintArgs = []string{"run", "--allow-serial-runners", "--max-issues-per-linter=0", "--max-same-issues=0"}

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

// lintFailures splits the linter's output three ways: the findings on the
// run's own files, which fail the gate; the findings elsewhere in the
// packages it linted, which do not; and whether the output held a finding
// at all, which is what separates a linter that reported nothing about the
// change from one that could not run.
//
// A finding always names its own file and line, so unlike a compiler or a
// test there is nothing to trim: every line is already about some file.
//
// The gate lints whole packages because the linter type-checks whatever it
// is handed, so it reads a neighbor on every run and used to discard what
// it found there. A run that makes a sibling file stop compiling cleanly
// then passed every round and failed in CI. A neighbor's finding is an
// advisory rather than a failure because the gate cannot say whether this
// run caused it: telling the run would blame it for what it inherited, and
// separating the two needs the package's findings as they stood when the
// run started, which no gate is handed today.
func lintFailures(out []byte, changes []tool.Change) ([]TrimmedFailure, []TrimmedFailure, bool) {
	changed := changedPaths(changes)
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")

	// One type error anywhere in a linted package stops most linters from
	// running at all, so whatever else the output holds is not a reading of
	// the change. The build gate reports the error in the same round.
	for _, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), typecheckSuffix) {
			return nil, nil, false
		}
	}

	var mine, neighbors []string

	for _, line := range lines {
		switch {
		case namesAChangedFile(line, changed):
			mine = append(mine, strings.TrimSpace(line))
		case findingLine.MatchString(strings.TrimSpace(line)):
			neighbors = append(neighbors, strings.TrimSpace(line))
		}
	}

	if len(mine) == 0 && len(neighbors) == 0 {
		return []TrimmedFailure{{Test: lintGateName, Context: headOf(lines)}}, nil, false
	}

	return bounded(mine), bounded(neighbors), true
}

// otherWriter is what a neighbor's finding is attributed to. The gate cannot
// name the thread that left it, and saying it belongs to somebody is the part
// that matters: a run that reads a finding as its own fixes it, and one told
// it is another writer's works around it.
const otherWriter = "another writer"

// bounded is one TrimmedFailure holding at most maxLintFindings of
// findings, saying how many it left out.
func bounded(findings []string) []TrimmedFailure {
	if len(findings) == 0 {
		return nil
	}

	if dropped := len(findings) - maxLintFindings; dropped > 0 {
		findings = append(slices.Clone(findings[:maxLintFindings]),
			fmt.Sprintf("(%d more not shown)", dropped))
	}

	return []TrimmedFailure{{Test: lintGateName, Frames: findings}}
}

// findingLine matches the head of a golangci-lint finding,
// `path:line:col: message (linter)`.
var findingLine = regexp.MustCompile(`^(.+?):\d+:\d+: `)

// namesAChangedFile reports whether a line is a finding about one of the
// changed files. It matches the path a finding opens with rather than the
// path anywhere in the line, and it matches by suffix.
//
// Both halves are load-bearing. Anywhere in the line reads the linter's own
// diagnostics as findings: a workspace under a symlinked root turned one
// `level=warning` line quoting a result.Issue struct into a 900-byte finding
// that a run then spent a turn explaining away. And the path a finding opens
// with is resolved against the linter's idea of the working directory, which
// under such a root is `../../<temp>/internal/bench/stats.go` for a change
// set holding `internal/bench/stats.go`, so an equality test drops every real
// finding while keeping that warning.
func namesAChangedFile(line string, changed []string) bool {
	match := findingLine.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return false
	}

	reported := filepath.ToSlash(filepath.Clean(match[1]))

	for _, path := range changed {
		if reported == path || strings.HasSuffix(reported, "/"+path) {
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
