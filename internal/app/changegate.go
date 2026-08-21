package app

import (
	"context"
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
// change events rather than on the model deciding to test, a passing gate
// tells the model nothing, and a failing one hands over only the failures
// that touch the change.
type ChangeGate struct {
	runner  *gate.Runner
	inbox   chan tool.Change
	pending []gate.Result
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

// Enqueue records one change for gating. It blocks only when the runner is
// far enough behind to fill the inbox, which is backpressure rather than a
// stall worth avoiding.
func (g *ChangeGate) Enqueue(c tool.Change) {
	g.inbox <- c
}

// Collect records one batch's failing gates. Start calls it; it is exported
// so a test can drive the formatting without a runner.
func (g *ChangeGate) Collect(res gate.RunResult) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i := range res.Gates {
		if !res.Gates[i].Pass {
			g.pending = append(g.pending, res.Gates[i])
		}
	}
}

// TakeFeedback returns what the gates found since the last call and clears
// it, empty when every gate passed. A passing gate is deliberately silent:
// telling the model a check passed spends tokens to say nothing happened.
func (g *ChangeGate) TakeFeedback() string {
	g.mu.Lock()
	results := g.pending
	g.pending = nil
	g.mu.Unlock()

	if len(results) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Gates ran on your changes and found this:\n")

	for i := range results {
		b.WriteString("\n" + describeFailure(results[i]))
	}

	b.WriteString("\nFix the cause before continuing.")

	return b.String()
}

// describeFailure renders one failing gate. A failure the gate could not
// describe still names the gate and says so, because handing the model a
// bare gate name and nothing else is worse than saying nothing: observed
// on a build failure whose frames did not survive trimming, the model was
// told `go-test` with no detail and spent 26 turns guessing at it.
func describeFailure(r gate.Result) string {
	var b strings.Builder

	said := false

	for _, f := range r.Failures {
		if len(f.Frames) == 0 && f.Test == "" && f.Package == "" {
			continue
		}

		b.WriteString(r.Gate + " " + failureName(f) + "\n")
		said = true

		if len(f.Frames) == 0 {
			b.WriteString("  no output line named a changed file, so run it yourself to see\n")
		}

		for _, frame := range f.Frames {
			b.WriteString("  " + frame + "\n")
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
