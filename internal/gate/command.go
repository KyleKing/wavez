package gate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/glob"
	"github.com/kyleking/wavez/internal/tool"
)

// CommandCheck is one check a project declares for itself: a name, the paths
// whose change runs it, the directory it runs in, and the command line that
// runs it. Rewrites marks a check that edits the files it looks at, which is
// a formatter.
type CommandCheck struct {
	Name     string
	Command  string
	Dir      string
	Paths    []string
	Rewrites bool
}

// CommandGate runs one command a project declared, when the change set holds
// a file it names.
//
// Every other gate here speaks Go, so a project in another language reaches
// the model with nothing behind it: pointed at a Python repository, the four
// Go gates abstain and an edit lands unverified. A project that can name its
// own check gets the same change-triggered loop the Go gates get, including
// the attribution that trims a failure to the files the run touched.
type CommandGate struct {
	repoRoot string
	check    CommandCheck
}

// NewCommandGates builds one gate per check, and returns nothing when a
// project declared none, so a project that configured nothing pays nothing.
func NewCommandGates(repoRoot string, checks []CommandCheck) []Gate {
	out := make([]Gate, 0, len(checks))

	for _, c := range checks {
		if c.Name == "" || c.Command == "" {
			continue
		}

		out = append(out, &CommandGate{repoRoot: repoRoot, check: c})
	}

	return out
}

// Name identifies this gate in the gate log, as the project named it.
func (g *CommandGate) Name() string { return g.check.Name }

// Resources reports the command's own name, so two changes do not run the
// same project command at once while different commands still overlap. A
// check that rewrites the worktree takes it exclusively instead, which is
// what runs a formatter to completion before anything reads what it wrote.
func (g *CommandGate) Resources() []string {
	if g.check.Rewrites {
		return []string{WorktreeResource}
	}

	return []string{"check:" + g.check.Name}
}

// Run executes the command when the change set holds a path this check
// names. A non-zero exit is reported through Result, since that is what
// reaches the model; only a command that could not be started is an error.
func (g *CommandGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	matched := g.matching(rc.Changes)
	if len(matched) == 0 {
		return Abstained(g.Name(), rc.Selection.Level,
			"the change set holds no path this check names"), nil
	}

	dir := filepath.Join(g.repoRoot, filepath.FromSlash(g.check.Dir))

	//nolint:gosec // the command line comes from the project's own configuration, like every other gate's
	cmd := exec.CommandContext(ctx, "sh", "-c", expandFiles(g.check.Command, matched, g.check.Dir))
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err == nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: len(matched), Pass: true}, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Result{Gate: g.Name(), Level: rc.Selection.Level},
			fmt.Errorf("running the %s check: %w", g.check.Name, err)
	}

	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	failure := TrimFailure(FailedTest{Name: g.check.Name, Output: lines}, changedPaths(rc.Changes))

	return Result{
		Gate:     g.Name(),
		Level:    rc.Selection.Level,
		Examined: len(matched),
		Failures: []TrimmedFailure{failure},
	}, nil
}

// filesPlaceholder is what a command writes where it wants the changed files
// this check matched.
const filesPlaceholder = "{files}"

// expandFiles substitutes the changed files into the command, each quoted and
// relative to the directory the command runs in.
//
// A check without the placeholder runs over whatever its command names, which
// is what a test suite wants. One with it runs over the change set, which is
// what a formatter wants: `ruff format .` in a repository of any size
// rewrites files the run never touched, and every one of them lands in the
// diff a human then has to read.
func expandFiles(command string, matched []string, dir string) string {
	if !strings.Contains(command, filesPlaceholder) {
		return command
	}

	quoted := make([]string, 0, len(matched))

	for _, m := range matched {
		rel := m
		if dir != "" && dir != "." {
			if r, err := filepath.Rel(dir, m); err == nil {
				rel = r
			}
		}

		quoted = append(quoted, shellQuote(filepath.ToSlash(rel)))
	}

	return strings.ReplaceAll(command, filesPlaceholder, strings.Join(quoted, " "))
}

// shellQuote wraps a path so `sh -c` reads it as one word whatever it holds.
func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// matching is the changed paths this check names. A check with no paths
// names every change, which is what a whole-project command wants.
func (g *CommandGate) matching(changes []tool.Change) []string {
	out := make([]string, 0, len(changes))

	for _, c := range changes {
		if g.names(c.Path) {
			out = append(out, c.Path)
		}
	}

	return out
}

// names reports whether one path fires this check. The pattern is matched
// against the whole path and then against its base name, which is what lets
// a project write `*.py` and mean every Python file rather than only the
// ones at the root.
func (g *CommandGate) names(path string) bool {
	if len(g.check.Paths) == 0 {
		return true
	}

	for _, pattern := range g.check.Paths {
		if glob.Match(pattern, filepath.ToSlash(path)) {
			return true
		}
	}

	return false
}
