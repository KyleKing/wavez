package app

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// changeInbox buffers change events between the tool that produced them and
// the gate runner's debounce loop. It is generous because a full inbox
// stalls the edit path, and small enough that a runner falling far behind
// applies backpressure instead of growing without bound.
const changeInbox = 256

// ChangeGate feeds a gate.Runner from the edit tools and holds what its
// gates found until a turn collects it. It exists so the two directions
// decouple: a gate run takes seconds, and an edit must not wait on one.
//
// DESIGN.md's Gates section is the contract it implements: gates trigger on
// change events rather than on the model deciding to test, and a failing gate
// hands over only the failures that touch the change. A passing gate is one
// line naming what passed, because silence is not free: a run that cannot
// tell whether the harness checked its edit checks them itself, and 20 of one
// run's 29 shell calls were hand-written `go build`, `go test`, `go vet`, and
// `gofmt` over changes the gates had already examined.
type ChangeGate struct {
	runner  *gate.Runner
	inbox   chan tool.Change
	pending []gate.Result
	// latest survives TakeFeedback, because a run asks "are the checks
	// green" long after it was told, and queued counts the changes no batch
	// has covered yet, which is the difference between a stale answer and a
	// current one.
	latest []gate.Result
	// changed is every path this run has written, in the order the changes
	// landed. It answers the question 24 of 278 logged shell calls asked jj
	// and git, and it is per run because the answer to "what have I changed"
	// is about this run and no other.
	changed []tool.Change
	queued  int
	mu      sync.Mutex
}

// NewChangeGate builds a ChangeGate over runner. Nothing flows until Start
// runs.
func NewChangeGate(runner *gate.Runner) *ChangeGate {
	return &ChangeGate{runner: runner, inbox: make(chan tool.Change, changeInbox)}
}

// Start runs the runner and the two pumps around it until ctx is done.
func (g *ChangeGate) Start(ctx context.Context) {
	go g.runner.Start(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-g.inbox:
				g.runner.Enqueue(c)
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case res := <-g.runner.Results():
				g.Collect(res)
			}
		}
	}()
}

// Begin forgets the previous run. The gate results go with it: a report
// about a tree two runs ago is worse than saying nothing.
func (g *ChangeGate) Begin() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.latest, g.changed, g.queued = nil, nil, 0
}

// Changed is every file this run has written, most recent last.
func (g *ChangeGate) Changed() []tool.Change {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Clone(g.changed)
}

// Enqueue records one change for gating. It blocks only when the runner is
// far enough behind to fill the inbox, which is backpressure rather than a
// stall worth avoiding.
func (g *ChangeGate) Enqueue(c tool.Change) {
	g.mu.Lock()
	g.queued++
	g.changed = append(g.changed, c)
	g.mu.Unlock()

	g.inbox <- c
}

// Collect records one batch's failing gates. Start calls it; it is exported
// so a test can drive the formatting without a runner.
func (g *ChangeGate) Collect(res gate.RunResult) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.pending = append(g.pending, res.Gates...)
	g.latest = res.Gates

	g.queued -= len(res.Changes)
	if g.queued < 0 {
		g.queued = 0
	}
}

// Status says what the harness's own gate runs already establish about the
// tree, and whether it can say anything at all. It is what lets the shell
// tool answer a command that re-runs the project's checks instead of
// running it: the prose asking a model not to has been in the system prompt
// since this type shipped, and 37 of 278 logged shell calls ran them
// anyway.
func (g *ChangeGate) Status() (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.queued > 0 {
		return "they are running on your latest changes now, and what they find " +
			"reaches you before your next turn", true
	}

	var passed []string

	for i := range g.latest {
		if !g.latest[i].Pass {
			return "they ran on your changes and failed, and you were told what they found", true
		}

		if g.latest[i].Examined > 0 {
			passed = append(passed, g.latest[i].Gate)
		}
	}

	if len(passed) == 0 {
		return "", false
	}

	return "they ran on your changes and passed: " + strings.Join(dedupe(passed), ", "), true
}

// TakeFeedback returns what the gates found since the last call and clears
// it, empty when no gate ran at all. A pass is one line naming the gates
// that examined the change, which is what keeps a run from re-running them
// through the shell; a failure carries the trimmed frames as well.
func (g *ChangeGate) TakeFeedback() string {
	g.mu.Lock()
	results := g.pending
	g.pending = nil
	g.mu.Unlock()

	if len(results) == 0 {
		return ""
	}

	var passed []string
	var failed []gate.Result

	for i := range results {
		switch {
		case !results[i].Pass:
			failed = append(failed, results[i])
		case results[i].Examined > 0:
			passed = append(passed, results[i].Gate)
		}
	}

	var b strings.Builder

	if len(failed) == 0 {
		if len(passed) == 0 {
			return ""
		}

		return "Gates ran on your changes and passed: " + strings.Join(dedupe(passed), ", ") +
			". Do not re-run these yourself."
	}

	b.WriteString("Gates ran on your changes and found this:\n")

	for i := range failed {
		b.WriteString("\n" + describeFailure(failed[i]))
	}

	b.WriteString("\nFix the cause before continuing.")

	return b.String()
}

// dedupe keeps the first occurrence of each name, since one turn's edits can
// trigger several batches over the same gates.
func dedupe(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))

	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}

	return out
}

// describeFailure renders one failing gate. A failure the gate could not
// attribute to a changed file falls back to the head of what the command
// printed, because handing the model a bare gate name and nothing else is
// worse than saying nothing: observed on a build failure whose frames did
// not survive trimming, the model was told `go-test` with no detail and
// spent 26 turns guessing at it.
func describeFailure(r gate.Result) string {
	var b strings.Builder

	said := false

	for _, f := range r.Failures {
		if len(f.Frames) == 0 && len(f.Context) == 0 && f.Test == "" && f.Package == "" {
			continue
		}

		b.WriteString(r.Gate + " " + failureName(f) + "\n")
		said = true

		lines := f.Frames
		if len(lines) == 0 {
			if len(f.Context) == 0 {
				b.WriteString("  no output line named a changed file, so run it yourself to see\n")
			} else {
				b.WriteString("  no output line named a changed file. What it printed:\n")
			}

			lines = f.Context
		}

		for _, line := range lines {
			b.WriteString("  " + line + "\n")
		}
	}

	if !said {
		b.WriteString(r.Gate + " failed without reporting which check, so run it yourself to see\n")
	}

	return b.String()
}

// failureName is the failing check's name, falling back to its package for
// a build failure, which go test reports with no test name at all.
func failureName(f gate.TrimmedFailure) string {
	if f.Test != "" {
		return f.Test
	}

	if f.Package != "" {
		return "build " + f.Package
	}

	return "build"
}
