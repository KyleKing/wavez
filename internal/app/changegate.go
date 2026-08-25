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
	// verdict is each gate's last pass/fail and how many changes this run
	// had made when it was recorded, which is what makes a false alarm
	// detectable: the same gate passing later over the same change set means
	// the first answer was about the harness rather than the code.
	verdict     map[string]gateVerdict
	falseAlarms []string
	queued      int
	mu          sync.Mutex
}

type gateVerdict struct {
	// failure is the gate's frames the last time it failed, which is what
	// makes "the same failure again" answerable. An identical one after the
	// run has edited says the run is not converging on it.
	failure string
	changes int
	repeats int
	pass    bool
}

// stuckAfter is how many identical gate failures, each after further edits,
// name a tier that is not going to fix this one. Three is what `e2` does on
// the fast tier: the same `undefined: Memory` compile error every round,
// quoted back each time, until the deadline. A run that fixes a failure in
// one or two attempts never reaches it.
const stuckAfter = 3

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
	g.verdict, g.falseAlarms = nil, nil
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

	g.noteFalseAlarms(res.Gates)

	g.queued -= len(res.Changes)
	if g.queued < 0 {
		g.queued = 0
	}
}

// noteFalseAlarms records every gate in results that passes over the same
// change set it just failed over. `h5` was exactly that and it cost three
// re-runs to name: nothing about the code had changed, so the failure was
// the harness's and the pass is the proof.
func (g *ChangeGate) noteFalseAlarms(results []gate.Result) {
	if g.verdict == nil {
		g.verdict = make(map[string]gateVerdict, len(results))
	}

	for i := range results {
		name := results[i].Gate

		prior, seen := g.verdict[name]
		if seen && !prior.pass && results[i].Pass && prior.changes == len(g.changed) {
			g.falseAlarms = append(g.falseAlarms, name)
		}

		next := gateVerdict{pass: results[i].Pass, changes: len(g.changed)}
		if !results[i].Pass {
			next.failure = failureSignature(results[i])
		}

		// Only a failure the run has edited against counts: the same tree
		// gets the same answer, and blaming the tier for that would fire on
		// every debounced re-run of one change. A re-run carries the count
		// rather than clearing it, because a batch that says nothing new is
		// not evidence the run has started converging.
		if seen && next.failure != "" && next.failure == prior.failure {
			next.repeats = prior.repeats
			if next.changes > prior.changes {
				next.repeats++
			}
		}

		g.verdict[name] = next
	}
}

// Stuck names a gate that has failed identically stuckAfter times, each
// after the run made further changes, or reports false. It is a routing
// signal rather than feedback: the run has been told what is wrong and has
// edited against it repeatedly without moving it, which is what a tier
// reaching the end of its remit looks like from the outside.
func (g *ChangeGate) Stuck() (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for name, v := range g.verdict {
		if v.repeats >= stuckAfter-1 {
			return name, true
		}
	}

	return "", false
}

// failureSignature is what a gate reported, flattened. Two rounds match
// only when every frame of every failure does, so a compile error that
// moved to another line is progress rather than a repeat.
func failureSignature(r gate.Result) string {
	parts := make([]string, 0, len(r.Failures))
	for _, f := range r.Failures {
		parts = append(parts, f.Package+" "+f.Test+"\n"+
			strings.Join(f.Frames, "\n")+"\n"+strings.Join(f.Context, "\n"))
	}

	return strings.Join(parts, "\n--\n")
}

// FalseAlarms returns the gates that have retracted a failure since the last
// call, and clears them.
func (g *ChangeGate) FalseAlarms() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := g.falseAlarms
	g.falseAlarms = nil

	return out
}

// Status says what the harness's own gate runs already establish about the
// tree, and whether it can say anything at all. It is what lets the shell
// tool answer a command that re-runs the project's checks instead of
// running it: the prose asking a model not to has been in the system prompt
// since this type shipped, and 37 of 278 logged shell calls ran them
// anyway.
//
// A failure repeats what the gates found rather than pointing at the turn
// that carried it. Feedback is delivered once and a run that has since
// compacted or simply moved on has no way back to it, so one `h6` run spent
// six turns trying to establish a build state it had already been told and
// could not re-read.
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
			return "they ran on your changes and failed:\n\n" + failureReport(g.latest), true
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

// failureReport renders every failing gate in results, which is what a run
// asking whether the build is fixed needs in front of it.
func failureReport(results []gate.Result) string {
	var b strings.Builder

	for i := range results {
		if !results[i].Pass {
			b.WriteString(describeFailure(results[i]))
		}
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// TakeFeedback returns what the gates found since the last call and clears
// it, empty when no gate ran at all, along with whether what it returns
// reports a failure. A pass is one line naming the gates that examined the
// change, which is what keeps a run from re-running them through the shell;
// a failure carries the trimmed frames as well.
func (g *ChangeGate) TakeFeedback() (string, bool) {
	g.mu.Lock()
	results := g.pending
	g.pending = nil
	g.mu.Unlock()

	if len(results) == 0 {
		return "", false
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
			return "", false
		}

		return "Gates ran on your changes and passed: " + strings.Join(dedupe(passed), ", ") +
			". Do not re-run these yourself.", false
	}

	b.WriteString("Gates ran on your changes and found this:\n")

	for i := range failed {
		b.WriteString("\n" + describeFailure(failed[i]))
	}

	if attributed(failed) {
		b.WriteString("\nFix the cause before continuing.")
	} else {
		b.WriteString("\nNone of this names a file this run changed. Decide whether the change " +
			"caused it before treating it as yours, and carry on with the task if it did not.")
	}

	return b.String(), true
}

// attributed reports whether any failure named a line in a file the run
// changed. A batch where none did is usually a failure the run inherited,
// and telling the model to fix the cause of one sends it hunting: one lane
// was handed a cleanup race in a package it had never opened, spent every
// remaining turn on it, and died stagnant with its own edit already correct.
func attributed(failed []gate.Result) bool {
	for i := range failed {
		for _, f := range failed[i].Failures {
			if len(f.Frames) > 0 {
				return true
			}
		}
	}

	return false
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
