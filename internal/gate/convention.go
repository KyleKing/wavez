package gate

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/kyleking/wavez/internal/astgrep"
)

// maxConventionFrames bounds how many violations one gate run hands back.
// A rule that matches everywhere would otherwise flood the model with the
// same message; the count of what was dropped goes in the last frame.
const maxConventionFrames = 20

// ConventionGate runs a project's ast-grep rules over the changed files.
// It sits between the formatter and the test gate in DESIGN.md's gate
// order, since a convention violation is cheaper to find than a test
// failure and its fix often changes what the tests would run against.
//
// A missing ast-grep binary is a failure, not a pass: a project that
// configured rules and cannot run them has no convention enforcement, and
// reporting that as green is the one outcome worse than reporting it as red.
type ConventionGate struct {
	runner   *astgrep.Runner
	repoRoot string
	rules    []astgrep.RuleFile
}

// NewConventionGate builds a gate over rules, or returns nil when there are
// none, so a project that configured no rules pays nothing rather than
// running an empty scan on every edit.
func NewConventionGate(repoRoot string, rules []astgrep.RuleFile, runner *astgrep.Runner) *ConventionGate {
	if len(rules) == 0 {
		return nil
	}

	if runner == nil {
		runner = astgrep.NewRunner()
	}

	return &ConventionGate{repoRoot: repoRoot, rules: rules, runner: runner}
}

// Name identifies this gate in the gate log.
func (*ConventionGate) Name() string { return "convention" }

// Resources reports no exclusive resource: the scan only reads the working
// tree, so it runs alongside the test and format gates.
func (*ConventionGate) Resources() []string { return nil }

// Run scans the changed files against every rule. It returns a non-nil
// error only when ast-grep could not be invoked at all; a rule match is
// reported through Result, since that is what a Verifier feeds back to the
// model.
func (g *ConventionGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	targets := g.targets(rc)
	if len(targets) == 0 {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: true}, nil
	}

	report, err := g.runner.Scan(ctx, g.repoRoot, g.rules, targets...)
	if err != nil {
		return Result{Gate: g.Name(), Level: rc.Selection.Level}, fmt.Errorf("ast-grep scan: %w", err)
	}

	if len(report.Findings) == 0 {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: len(targets), Pass: true}, nil
	}

	return Result{
		Gate:     g.Name(),
		Level:    rc.Selection.Level,
		Examined: len(targets),
		Failures: groupByRule(report.Findings),
	}, nil
}

// targets passes each changed path to ast-grep exactly as tool.Change
// carries it, repo-relative, because the scan already runs with the repo
// root as its working directory.
//
// Absolute paths must never be substituted here. A rule's `files:` globs
// are matched against the path ast-grep is given, so an absolute target
// makes every scoped rule match nothing and the gate reports a pass it
// never earned.
func (g *ConventionGate) targets(rc RunContext) []string {
	out := make([]string, 0, len(rc.Changes))

	for _, c := range rc.Changes {
		if c.Path == "" {
			continue
		}

		rel := c.Path
		if filepath.IsAbs(rel) {
			if r, err := filepath.Rel(g.repoRoot, rel); err == nil {
				rel = r
			}
		}

		out = append(out, filepath.ToSlash(rel))
	}

	return out
}

// groupByRule turns findings into one TrimmedFailure per rule id, which is
// the shape a failing rule shares with a failing test: a name plus the
// lines that name it. Rules are ordered by id so a run is reproducible.
func groupByRule(findings []astgrep.Finding) []TrimmedFailure {
	byRule := make(map[string][]string)

	for i := range findings {
		m := astgrep.TrimForModel(findings[i])

		frame := fmt.Sprintf("%s: %s", m.Location, m.Message)
		if m.Fix != "" {
			frame += fmt.Sprintf(" (fix: %s)", m.Fix)
		}

		byRule[m.RuleID] = append(byRule[m.RuleID], frame)
	}

	ids := make([]string, 0, len(byRule))
	for id := range byRule {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	out := make([]TrimmedFailure, 0, len(ids))

	for _, id := range ids {
		frames := byRule[id]
		if len(frames) > maxConventionFrames {
			dropped := len(frames) - maxConventionFrames
			frames = append(frames[:maxConventionFrames:maxConventionFrames],
				fmt.Sprintf("... [%d more violations of %s] ...", dropped, id))
		}

		out = append(out, TrimmedFailure{Test: id, Frames: frames})
	}

	return out
}
