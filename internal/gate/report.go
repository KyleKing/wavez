// Package gate runs the checks a set of changed files selects and reports
// what they found in the form a model reads: nothing on a pass, the failing
// lines on a failure, and an explicit abstention when a check could not run.
package gate

import "context"

// Stage says when a check runs. An edit-stage check runs after every write
// and has to answer in well under a second; a stop-stage check runs once
// before the agent is allowed to finish its turn.
type Stage string

// The stages a check can declare.
const (
	StageEdit Stage = "edit"
	StageStop Stage = "stop"
)

// Status is what one check concluded.
type Status string

// A check that could not run is StatusAbstained, never StatusFail: a missing
// binary or a toolchain error is not something the agent's change caused, and
// handing it over as a failure sends the agent to fix the harness.
const (
	StatusPass      Status = "pass"
	StatusFail      Status = "fail"
	StatusAbstained Status = "abstained"
	// StatusPreexisting is a failure the tree the session started from
	// already had. It does not block and the agent never hears of it: the
	// session did not cause it, so it is not the session's to fix.
	StatusPreexisting Status = "preexisting"
)

// Result is one check's outcome.
//
//nolint:govet // fieldalignment: the field order is the JSON key order of a frozen contract
type Result struct {
	Check string `json:"check"`
	// Blocking is false for an advisory check, whose failure is reported
	// and never holds the agent.
	Blocking bool   `json:"blocking"`
	Status   Status `json:"status"`
	// Summary is the reduced output on a failure and the reason on an
	// abstention. It is empty on a pass.
	Summary string `json:"summary,omitempty"`
	// Fixed lists the files the check's own fixer rewrote, relative to the
	// root. The agent's view of those files is stale.
	Fixed []string `json:"fixed,omitempty"`
	// Preexisting names the failures the tree the session started from
	// already had. They are for the person. A result that is still a
	// failure carries the rest in Summary.
	Preexisting []string `json:"preexisting,omitempty"`
	// Flaky names the tests that failed and then passed when run again on
	// the same tree. They are for the person and do not block.
	Flaky []string `json:"flaky,omitempty"`
	// Note is for the person: why a failure could not be compared with the
	// tree the session started from, and so still blocks.
	Note string `json:"note,omitempty"`
	// evidence is what a failure keeps for that comparison.
	evidence *evidence
}

// Report is every result of one run.
type Report struct {
	Results []Result `json:"results"`
}

// Runner runs the checks for a root. It is the surface internal/hook and
// cmd/wavez consume, and its signature is frozen.
//
// The changed paths are relative to root. An empty changed set is a run that
// examined nothing and returns an empty Report. A context from WithBaseline
// makes a stop-stage run set aside the failures that baseline already had.
type Runner interface {
	Run(ctx context.Context, root string, changed []string, stage Stage) (Report, error)
}
