package gate

import (
	"context"
	"time"

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
	cadence := &fullRunCadence{clock: clock, cfg: DefaultCadence, lastFull: clock.Now()}

	return func(ctx context.Context, changes []tool.Change) RunResult {
		selection, err := Select(ctx, cov, graph, changes)
		if err != nil {
			selection = Selection{Level: LevelPackage, Packages: fallbackPackages(graph, changes)}
		}

		selection = cadence.apply(selection)

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

// DefaultCadence bounds how long selection may keep narrowing. Both numbers
// are the cost of being wrong in the cheap direction: a selected set that
// misses a caller is only found by a run that does not select, and the
// alternative to a bound is a whole session of green gates over a build
// nothing swept.
var DefaultCadence = CadenceConfig{
	MaxSelectivePasses: maxSelectivePasses,
	MaxInterval:        maxSelectiveInterval,
}

const (
	maxSelectivePasses   = 10
	maxSelectiveInterval = 15 * time.Minute
)

// wholeModule is what a forced full run examines.
const wholeModule = "./..."

// fullRunCadence forces a sweep once selection has narrowed for long
// enough. Only a narrowed batch counts against it, because a batch that
// already fell back to whole packages has not been narrowed.
type fullRunCadence struct {
	clock    Clock
	lastFull time.Time
	cfg      CadenceConfig
	passes   int
}

func (c *fullRunCadence) apply(selection Selection) Selection {
	// The coverage map has no untracked-file flag to read, so the two
	// thresholds are what decide; Select already falls back to whole
	// packages for a file it cannot resolve.
	if !NeedsFullRun(c.cfg, c.passes, c.clock.Now().Sub(c.lastFull), false) {
		if selection.Level != LevelPackage {
			c.passes++
		}

		return selection
	}

	c.passes = 0
	c.lastFull = c.clock.Now()

	return Selection{Level: LevelPackage, Packages: []string{wholeModule}}
}
