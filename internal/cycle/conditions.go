package cycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/condition"
	"github.com/kyleking/wavez/internal/tool"
)

// Names of the exit conditions a project may put on a phase in
// ".wavez.pkl". They are code rather than configuration because a Condition
// the harness cannot evaluate is a claim, and wavez ships no plugin system.
const (
	// CondArtifactFails holds once the change set declares a test that fails
	// on the tree as it stands.
	CondArtifactFails = "artifact-fails"
	// CondArtifactPassesGated holds once every test the change set declares
	// passes and the phase's own gates were green.
	CondArtifactPassesGated = "artifact-passes-gates-green"
	// CondSweepAccounted holds once every hit of a recorded sweep is fixed or
	// dismissed with a reason, or the sweep is shown not to discriminate and a
	// durable artifact is named in its place.
	CondSweepAccounted = "sweep-accounted"
)

// Observation is what re-running one test on the current tree showed.
type Observation struct {
	Package string
	Test    string
	Detail  string
	Failed  bool
}

// Prober re-runs the tests a change set declares against the tree as it
// stands. It is what turns "there is a failing test" from a claim into a
// reading, so a Prober that cannot run returns an error rather than an empty
// result: a check that did not run has not passed.
type Prober interface {
	Probe(ctx context.Context, repoRoot string, changes []tool.Change) ([]Observation, error)
}

// Hit is one site a sweep matched.
type Hit struct {
	File string
	Line int
}

// Sweeper re-runs a recorded structural sweep, so the work list the
// generalize phase triages comes from the harness rather than from the
// model's recall.
type Sweeper interface {
	Sweep(ctx context.Context, repoRoot string, s Sweep) ([]Hit, error)
}

// Checks are the deterministic backends the built-in conditions evaluate
// against.
type Checks struct {
	Prober  Prober
	Sweeper Sweeper
}

// Conditions returns every exit condition a phase may name, keyed by the
// name a ".wavez.pkl" phase writes.
func Conditions(c Checks) map[string]condition.Condition[State] {
	return map[string]condition.Condition[State]{
		CondArtifactFails:       ArtifactFails(c.Prober),
		CondArtifactPassesGated: condition.All(CondArtifactPassesGated, ArtifactPasses(c.Prober), GatesGreen()),
		CondSweepAccounted:      SweepAccounted(c.Sweeper),
	}
}

// ArtifactFails holds once the Cycle's change set declares at least one test
// and that test fails on the current tree. A phase that wrote no test has
// produced nothing the harness can re-run, which is not the same as a
// reproduction that failed, so both report the reason rather than only a
// refusal.
func ArtifactFails(p Prober) condition.Condition[State] {
	return condition.Func(CondArtifactFails, func(ctx context.Context, s State) (condition.Verdict, error) {
		observed, err := p.Probe(ctx, s.RepoRoot, s.Changes)
		if err != nil {
			return condition.Verdict{}, fmt.Errorf("probing the change set's tests: %w", err)
		}

		if len(observed) == 0 {
			return condition.Unmet(CondArtifactFails,
				"the change set declares no test on its changed lines, so it produced no artifact to fail"), nil
		}

		if failed := failing(observed); len(failed) > 0 {
			return condition.Met(CondArtifactFails,
				strings.Join(failed, ", ")+" fails on the current tree"), nil
		}

		return condition.Unmet(CondArtifactFails, fmt.Sprintf(
			"all %d test(s) the change set declares pass on the current tree, so nothing is reproduced",
			len(observed),
		)), nil
	})
}

// ArtifactPasses holds once every test the change set declares passes on the
// current tree.
func ArtifactPasses(p Prober) condition.Condition[State] {
	return condition.Func("artifact-passes", func(ctx context.Context, s State) (condition.Verdict, error) {
		observed, err := p.Probe(ctx, s.RepoRoot, s.Changes)
		if err != nil {
			return condition.Verdict{}, fmt.Errorf("probing the change set's tests: %w", err)
		}

		if len(observed) == 0 {
			return condition.Unmet("artifact-passes",
				"the change set declares no test, so there is no artifact to pass"), nil
		}

		if failed := failing(observed); len(failed) > 0 {
			return condition.Unmet("artifact-passes",
				strings.Join(failed, ", ")+" still fails"), nil
		}

		return condition.Met("artifact-passes",
			fmt.Sprintf("all %d test(s) the change set declares pass", len(observed))), nil
	})
}

// GatesGreen holds when the phase's Loop ended on its own completion
// condition. For a gated phase that means the verification round passed,
// including the fail-to-pass check that the run's tests die when its
// non-test hunks are reverted.
func GatesGreen() condition.Condition[State] {
	return condition.Func("gates-green", func(_ context.Context, s State) (condition.Verdict, error) {
		if s.LoopComplete {
			return condition.Met("gates-green", "the phase's loop completed with its gates green"), nil
		}

		return condition.Unmet("gates-green", "the phase's loop stopped early: "+s.LoopReason), nil
	})
}

// SweepAccounted holds once a recorded sweep has every remaining hit
// dismissed with a reason, or the sweep is shown not to discriminate and a
// durable artifact is named and written instead. Fixing a hit removes it
// from the sweep, so the harness re-runs the pattern and asks only about
// what is still there.
func SweepAccounted(sw Sweeper) condition.Condition[State] {
	return condition.Func(CondSweepAccounted, func(ctx context.Context, s State) (condition.Verdict, error) {
		recorded, ok := s.Ledger.LastSweep()
		if !ok {
			return condition.Unmet(CondSweepAccounted,
				"no sweep was recorded, so nothing establishes where else the cause reaches"), nil
		}

		hits, err := sw.Sweep(ctx, s.RepoRoot, recorded)
		if err != nil {
			return condition.Verdict{}, fmt.Errorf("re-running the recorded sweep: %w", err)
		}

		left := untriaged(hits, recorded.Dismissed)
		if len(left) == 0 {
			return condition.Met(CondSweepAccounted, fmt.Sprintf(
				"the sweep leaves %d hit(s), each fixed or dismissed with a reason", len(hits),
			)), nil
		}

		if verdict, named := durableArtifact(recorded, s, len(hits), len(left)); named {
			return verdict, nil
		}

		return condition.Unmet(CondSweepAccounted, fmt.Sprintf(
			"%d of %d sweep hit(s) are neither fixed nor dismissed (%s) and no durable artifact is named",
			len(left), len(hits), strings.Join(left, ", "),
		)), nil
	})
}

// durableArtifact is SweepAccounted's second exit: a sweep that does not
// discriminate is answered by a file the Cycle wrote rather than by a work
// list. The file has to exist and has to be in the change set, because a
// named artifact nobody wrote is the same claim the Condition exists to
// refuse.
func durableArtifact(recorded Sweep, s State, hits, left int) (condition.Verdict, bool) {
	if recorded.Artifact == "" {
		return condition.Verdict{}, false
	}

	path := filepath.Join(s.RepoRoot, filepath.FromSlash(recorded.Artifact))
	if !within(s.RepoRoot, path) {
		return condition.Unmet(CondSweepAccounted,
			"the named durable artifact points outside the project"), true
	}

	if _, err := os.Stat(path); err != nil {
		return condition.Unmet(CondSweepAccounted,
			fmt.Sprintf("the named durable artifact %s does not exist", recorded.Artifact)), true
	}

	if !inChangeSet(s.Changes, recorded.Artifact) {
		return condition.Unmet(CondSweepAccounted, fmt.Sprintf(
			"the named durable artifact %s is not in this cycle's change set, so this run did not write it",
			recorded.Artifact,
		)), true
	}

	return condition.Met(CondSweepAccounted, fmt.Sprintf(
		"the sweep does not discriminate (%d hit(s), %d untriaged) and %s is written in its place",
		hits, left, recorded.Artifact,
	)), true
}

func failing(observed []Observation) []string {
	var out []string

	for _, o := range observed {
		if o.Failed {
			out = append(out, o.Test)
		}
	}

	return out
}

func untriaged(hits []Hit, dismissed []Dismissal) []string {
	var out []string

	for _, h := range hits {
		accounted := false

		for _, d := range dismissed {
			if sameSite(h, d) {
				accounted = true

				break
			}
		}

		if !accounted {
			out = append(out, fmt.Sprintf("%s:%d", h.File, h.Line))
		}
	}

	return out
}

// sameSite matches a dismissal to a hit by file, and by line when the
// dismissal named one. A dismissal with no line covers the file, since a
// fix elsewhere in it shifts every line below.
func sameSite(h Hit, d Dismissal) bool {
	return filepath.ToSlash(h.File) == filepath.ToSlash(d.File) && (d.Line == 0 || d.Line == h.Line)
}

func inChangeSet(changes []tool.Change, path string) bool {
	want := filepath.ToSlash(filepath.Clean(path))

	for _, c := range changes {
		if filepath.ToSlash(filepath.Clean(c.Path)) == want {
			return true
		}
	}

	return false
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
