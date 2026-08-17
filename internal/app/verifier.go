package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// GateVerifier is the agent.Verifier a real project wires in: it selects
// the narrowest test tier for the changes accumulated across a run, then
// runs its gates in order (format pre-pass, build, selected tests),
// stopping at the first failure so a broken compile never masks its own
// test output. Gates and their dependencies are injected so tests never
// shell out to a real toolchain.
type GateVerifier struct {
	cov      gate.LineCoverage
	clock    gate.Clock
	graph    *gate.ImportGraph
	log      *gate.Log
	repoRoot string
	gates    []gate.Gate
}

// NewGateVerifier builds a GateVerifier rooted at repoRoot, running gates
// in the given order against cov and graph for test selection.
func NewGateVerifier(
	repoRoot string, cov gate.LineCoverage, graph *gate.ImportGraph, log *gate.Log, clock gate.Clock, gates []gate.Gate,
) *GateVerifier {
	return &GateVerifier{repoRoot: repoRoot, cov: cov, graph: graph, log: log, clock: clock, gates: gates}
}

// Verify implements agent.Verifier.
func (v *GateVerifier) Verify(ctx context.Context, changes []tool.Change) (string, bool) {
	selection, err := gate.Select(ctx, v.cov, v.graph, changes)
	if err != nil {
		selection = gate.Selection{Level: gate.LevelPackage}
	}

	rc := gate.RunContext{RepoRoot: v.repoRoot, Changes: changes, Selection: selection}

	for _, g := range v.gates {
		result := v.runStep(ctx, g, rc)
		if !result.Pass {
			return feedbackText(result), false
		}
	}

	return "", true
}

// runStep runs one gate, stamping timing the way gate.RunGates does, and
// persists the outcome to the project's gate log. A gate that returns an
// error (rather than a failing Result) still produces feedback the model
// can act on, since an absent toolchain binary is as much a failure as a
// failing check.
func (v *GateVerifier) runStep(ctx context.Context, g gate.Gate, rc gate.RunContext) gate.Result {
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

func feedbackText(result gate.Result) string {
	view := result.ForModel()
	if view.Pass {
		return ""
	}

	lines := make([]string, 0, len(view.Failures))
	for _, f := range view.Failures {
		lines = append(lines, fmt.Sprintf("%s: %s", f.Test, strings.Join(f.Frames, "; ")))
	}

	return fmt.Sprintf("%s failed:\n%s", result.Gate, strings.Join(lines, "\n"))
}
