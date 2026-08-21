package gate

import (
	"context"

	"github.com/kyleking/wavez/internal/tool"
)

// BuildRunFunc composes selection, gate execution, and logging into the
// RunFunc a Runner invokes for each debounced batch: this is how a caller
// wires tool.Change events all the way through to a gate log entry. Graph
// may be nil, which drops every batch straight to LevelPackage. Res is the
// process's shared resource set, so a batch's `go test` waits on anything
// else already running under that key.
func BuildRunFunc(
	clock Clock, cov LineCoverage, graph *ImportGraph, gates []Gate, log *Log, repoRoot string, res *ResourceSet,
) RunFunc {
	return func(ctx context.Context, changes []tool.Change) RunResult {
		selection, err := Select(ctx, cov, graph, changes)
		if err != nil {
			selection = Selection{Level: LevelPackage, Packages: fallbackPackages(graph, changes)}
		}

		rc := RunContext{RepoRoot: repoRoot, Changes: changes, Selection: selection}
		results := RunGates(ctx, clock, res, gates, rc)

		var logErr error

		for i := range results {
			r := &results[i]
			if err := log.Append(LogEntry{
				Timestamp:  r.Timestamp,
				Gate:       r.Gate,
				Level:      r.Level,
				Duration:   r.Duration,
				Reason:     r.Reason,
				Advisories: r.Advisories,
				Examined:   r.Examined,
				Pass:       r.Pass,
			}); err != nil && logErr == nil {
				logErr = err
			}
		}

		return RunResult{Changes: changes, Gates: results, LogError: logErr}
	}
}
