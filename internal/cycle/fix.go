package cycle

import "fmt"

// FixCycle is the name of the built-in fix cycle.
const FixCycle = "fix"

// investigateTools is what a phase that reads, edits, and runs experiments
// may call. Narrowing is for routing rather than safety: shell stays behind
// the permission gate and the destructive-command guard either way.
var investigateTools = []string{
	"read", "search", "context", "delete", "rename", "str_replace", "write", "shell", "hypothesis",
}

// generalizeTools swap the shell for the sweep, since the generalize phase's
// work is triaging a list the harness produced rather than running an
// experiment.
var generalizeTools = []string{
	"read", "search", "context", "delete", "rename", "str_replace", "write", "sweep", "hypothesis",
}

// Fix returns the fix cycle wavez ships: reproduce, fix, generalize. Its
// first two conditions are the fail-to-pass property read in both
// directions, which is measurable today: over 30 commits of this repo's
// history, 16 of the 19 that touched Go code were fail-to-pass and none
// shipped a test that survives its own fix being undone.
func Fix(c Checks) Cycle {
	return Cycle{
		Name: FixCycle,
		Phases: []Phase{
			{
				Name: "reproduce",
				Goal: "Write the artifact that demonstrates the bug: a test that fails on the tree as it " +
					"stands. Do not fix anything yet. Record each candidate cause you test with the " +
					"hypothesis tool, including the ones you falsify.",
				Tools: investigateTools,
				Exit:  ArtifactFails(c.Prober),
			},
			{
				Name: "fix",
				Goal: "Make the failing test pass by fixing the cause, not the symptom. The test you wrote " +
					"must still fail when your fix is reverted, so do not weaken it.",
				Tools: investigateTools,
				Exit:  Conditions(c)[CondArtifactPassesGated],
				Gated: true,
			},
			{
				Name: "generalize",
				Goal: "Establish where else this cause reaches. Express it as one structural pattern and " +
					"call the sweep tool, then fix every hit or dismiss it with a reason. If the pattern " +
					"does not discriminate, name a durable artifact you wrote instead: a rule file, a " +
					"helper that makes the wrong call impossible, or a test at the boundary.",
				Tools: generalizeTools,
				Exit:  SweepAccounted(c.Sweeper),
				Gated: true,
			},
		},
	}
}

// Spec is one cycle as a project writes it in ".wavez.pkl". Exit names a
// built-in condition, since a Condition the harness cannot evaluate is a
// claim and wavez ships no plugin system.
type Spec struct {
	Name   string
	Phases []PhaseSpec
}

// PhaseSpec is one phase of a Spec.
type PhaseSpec struct {
	Name        string
	Goal        string
	Exit        string
	Tools       []string
	MaxAttempts int
	Gated       bool
}

// Resolve returns every cycle this project can run: the built-in ones, with
// any same-named Spec replacing one outright rather than merging into it, so
// a project's definition reads as written.
func Resolve(specs []Spec, c Checks) (map[string]Cycle, error) {
	out := map[string]Cycle{FixCycle: Fix(c)}
	known := Conditions(c)

	for _, spec := range specs {
		built := Cycle{Name: spec.Name}

		for _, p := range spec.Phases {
			exit, ok := known[p.Exit]
			if !ok {
				return nil, fmt.Errorf("%w: %q on %s/%s", ErrUnknownCondition, p.Exit, spec.Name, p.Name)
			}

			built.Phases = append(built.Phases, Phase{
				Name:        p.Name,
				Goal:        p.Goal,
				Tools:       p.Tools,
				Exit:        exit,
				MaxAttempts: p.MaxAttempts,
				Gated:       p.Gated,
			})
		}

		if err := built.Validate(); err != nil {
			return nil, err
		}

		out[spec.Name] = built
	}

	return out, nil
}
