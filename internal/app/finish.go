package app

import (
	"context"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/finish"
	"github.com/kyleking/wavez/internal/tool"
)

// FinishChecker runs every deterministic finish check over a completed run.
// It is what DESIGN.md's item 16 replaces the model reviewer with: four
// bounds, each able to fail a run on its own, and none of them a claim that
// the diff is correct.
type FinishChecker struct {
	index  finish.Index
	cov    finish.Coverage
	differ Differ
	opened finish.Opened
	root   string
}

// NewFinishChecker builds a checker rooted at root. A nil index or coverage
// map makes the checks that need it abstain rather than fail, since a
// workspace that never built one would otherwise fail every run for the
// workspace's reason.
func NewFinishChecker(
	root string, index finish.Index, cov finish.Coverage, differ Differ, opened finish.Opened,
) *FinishChecker {
	return &FinishChecker{root: root, index: index, cov: cov, differ: differ, opened: opened}
}

// Check implements agent.Finisher.
func (c *FinishChecker) Check(ctx context.Context, f agent.Finish) ([]string, error) {
	changed := paths(f.Changes)

	var reports []finish.Report

	named, err := finish.NamedThingsExist(ctx, c.root, f.Answer, c.index)
	if err != nil {
		return nil, err //nolint:wrapcheck // the check already names the lookup that failed
	}

	reports = append(reports, named)

	task, err := finish.ChangeSetMatchesTask(c.root, f.Task, changed)
	if err != nil {
		return nil, err //nolint:wrapcheck // the check already names the file that failed
	}

	reports = append(reports, task)

	if f.Goal != "" && f.Goal != f.Task {
		goal, gerr := finish.ChangeSetMatchesGoal(c.root, f.Goal, changed)
		if gerr != nil {
			return nil, gerr //nolint:wrapcheck // the check already names the file that failed
		}

		reports = append(reports, goal)
	}

	tested, err := finish.ChangedLinesAreTested(ctx, f.Changes, c.cov)
	if err != nil {
		return nil, err //nolint:wrapcheck // the check already names the file that failed
	}

	reports = append(reports, tested, c.substance(ctx, f, changed),
		finish.AnswerReadsWhatItNames(c.root, f.Answer, changed, c.opened))

	return findings(reports), nil
}

// substance reads what the run actually wrote. A diff that cannot be
// produced abstains: this bound exists to catch a run that did nothing, and
// failing one because version control was unavailable would say something
// about the machine instead.
func (c *FinishChecker) substance(ctx context.Context, f agent.Finish, changed []string) finish.Report {
	if c.differ == nil || f.Checkpoint == "" || len(changed) == 0 {
		return finish.Report{}
	}

	diff, err := c.differ.Diff(ctx, c.root, f.Checkpoint, changed)
	if err != nil {
		return finish.Report{}
	}

	return finish.ChangeHasSubstance(diff)
}

func findings(reports []finish.Report) []string {
	var out []string

	for _, r := range reports {
		for _, f := range r.Findings {
			out = append(out, f.Check+": "+f.Detail)
		}
	}

	return out
}

func paths(changes []tool.Change) []string {
	seen := make(map[string]bool, len(changes))
	out := make([]string, 0, len(changes))

	for _, c := range changes {
		if seen[c.Path] {
			continue
		}

		seen[c.Path] = true

		out = append(out, c.Path)
	}

	return out
}
