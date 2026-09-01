package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/tool"
)

// GateVerifier is the agent.Verifier a real project wires in: it selects
// the narrowest test tier for the changes accumulated across a run, then
// runs its gates in order (format pre-pass, build, selected tests),
// stopping at the first failure so a broken compile never masks its own
// test output. Gates and their dependencies are injected so tests never
// shell out to a real toolchain.
type GateVerifier struct {
	cov       gate.LineCoverage
	clock     gate.Clock
	graph     *gate.ImportGraph
	log       *gate.Log
	resources *gate.ResourceSet
	writers   Writers
	repoRoot  string
	gates     []gate.Gate
}

// Writers reports the threads writing into the tree besides the one being
// verified. A lease.Manager satisfies it.
type Writers interface {
	OtherActiveHolders(holder, dir string) []string
}

// NewGateVerifier builds a GateVerifier rooted at repoRoot, running gates
// in the given order against cov and graph for test selection. Resources
// is the project's shared resource set, so a verification round and
// anything else holding `go test` take turns; nil serializes with nothing.
func NewGateVerifier(
	repoRoot string, cov gate.LineCoverage, graph *gate.ImportGraph, log *gate.Log, clock gate.Clock,
	gates []gate.Gate, resources *gate.ResourceSet, writers Writers,
) *GateVerifier {
	return &GateVerifier{
		repoRoot: repoRoot, cov: cov, graph: graph, log: log, clock: clock,
		gates: gates, resources: resources, writers: writers,
	}
}

// Verify implements agent.Verifier.
func (v *GateVerifier) Verify(ctx context.Context, changes []tool.Change) (string, agent.GateVerdict) {
	selection, err := gate.Select(ctx, v.cov, v.graph, changes)
	if err != nil {
		selection = gate.Selection{Level: gate.LevelPackage}
	}

	rc := gate.RunContext{RepoRoot: v.repoRoot, Changes: changes, Selection: selection}

	for _, g := range v.gates {
		result := v.runStep(ctx, g, rc)
		if !result.Pass {
			if !gate.Attributable(result, v.graph, changes) {
				return unattributedText(result), agent.VerdictUnattributed
			}

			return feedbackText(result, v.soleWriter(ctx)), agent.VerdictFailed
		}
	}

	return "", agent.VerdictPass
}

// unattributedText frames a failure as the tree's rather than the run's, so
// a scheduler reading the outcome does not read it as a model that failed.
func unattributedText(result gate.Result) string {
	return "the tree fails a gate this run cannot have caused:\n" + feedbackText(result, false)
}

// soleWriter reports whether this run is the only thread writing the tree,
// which is what decides whether a finding on somebody else's line is worth
// saying. With another lane live the finding will have moved before the run
// could act on it, and alone in a dirty tree it was left by the user or an
// earlier run and stays where it is.
func (v *GateVerifier) soleWriter(ctx context.Context) bool {
	if v.writers == nil {
		return true
	}

	holder, _ := lease.HolderFrom(ctx)

	return len(v.writers.OtherActiveHolders(holder, v.repoRoot)) == 0
}

// runStep runs one gate, stamping timing the way gate.RunGates does, and
// persists the outcome to the project's gate log. A gate that returns an
// error (rather than a failing Result) still produces feedback the model
// can act on, since an absent toolchain binary is as much a failure as a
// failing check.
func (v *GateVerifier) runStep(ctx context.Context, g gate.Gate, rc gate.RunContext) gate.Result {
	release := v.resources.Lock(g.Resources())
	defer release()

	start := v.clock.Now()

	result, err := g.Run(ctx, rc)
	if err != nil {
		result = gate.Result{
			Gate:  g.Name(),
			Level: rc.Selection.Level,
			Failures: []gate.TrimmedFailure{
				{Test: g.Name(), Frames: []string{err.Error()}},
			},
		}
	}

	result.Timestamp = start
	result.Duration = v.clock.Now().Sub(start)

	entry := gate.LogEntry{
		Timestamp: result.Timestamp,
		Gate:      result.Gate,
		Level:     result.Level,
		Duration:  result.Duration,
		Reason:    result.Reason,
		Examined:  result.Examined,
		Pass:      result.Pass,
	}
	if logErr := v.log.Append(entry); logErr != nil {
		result.Pass = false
		result.Failures = append(result.Failures, gate.TrimmedFailure{
			Test: g.Name(), Frames: []string{fmt.Sprintf("gate log: %v", logErr)},
		})
	}

	return result
}

// feedbackText renders a failing gate for the model. A finding attributed to
// another writer rides along when this run is alone in the tree, named as
// theirs so the run works around it rather than claiming it. It never fails a
// gate on its own, because a run stopped over a line it did not write is the
// failure this whole path exists to end.
func feedbackText(result gate.Result, soleWriter bool) string {
	view := result.ForModel()
	if view.Pass {
		return ""
	}

	text := strings.TrimRight(describeFailure(result), "\n")
	if !soleWriter {
		return text
	}

	if attributed := attributedText(result); attributed != "" {
		return text + "\n\n" + attributed
	}

	return text
}

// attributedText renders the advisories that name a writer. Advisories
// without one grade the run's own work (a test that checks nothing, a mutant
// no test killed) and stay out, since a run told to fix one satisfies it by
// writing whatever silences it.
func attributedText(result gate.Result) string {
	var b strings.Builder

	for _, a := range result.Advisories {
		if a.Writer == "" {
			continue
		}

		if b.Len() == 0 {
			b.WriteString("Findings in the same packages that " + a.Writer +
				" left. They are not yours and they do not fail this gate, so work around them:\n")
		}

		for _, frame := range a.Frames {
			b.WriteString("  " + frame + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
