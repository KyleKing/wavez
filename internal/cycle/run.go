package cycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/condition"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// detailCycle and detailPhase key every event this package logs.
const (
	detailCycle = "cycle"
	detailPhase = "phase"
)

// Stop names why a Cycle ended.
type Stop string

// Reasons a Cycle stops.
const (
	// StopComplete means every phase's exit Condition held.
	StopComplete Stop = "complete"
	// StopConditionUnmet means a phase exhausted its attempts with its exit
	// Condition still not holding. The Cycle carries that reason rather than
	// reporting work nobody checked as finished.
	StopConditionUnmet Stop = "condition_unmet"
)

// PhaseResult is how one phase's model work ended, as the Driver reports it.
// Stop is the Loop's own stop Condition, which is the same Verdict shape a
// phase's exit Condition returns one granularity up.
type PhaseResult struct {
	Stop      condition.Verdict
	Changes   []tool.Change
	Turns     int
	ToolCalls int
	SpendUSD  float64
	Complete  bool
}

// Attempt is one phase run handed to a Driver.
type Attempt struct {
	Ledger *Ledger
	Cycle  string
	Prompt string
	Phase  Phase
	Number int
}

// Driver runs one phase's model work under the phase's tool set and reports
// how it ended. It is an interface here because the Cycle decides what a
// phase may see and the composition root decides how a model is reached.
type Driver interface {
	Drive(ctx context.Context, a Attempt) (PhaseResult, error)
}

// Log records a Cycle's phase transitions and Condition verdicts where a
// client is already watching.
type Log interface {
	Append(ev event.Event) (uint64, error)
}

// PhaseOutcome is what one phase did.
type PhaseOutcome struct {
	Phase     string
	Verdict   condition.Verdict
	SpendUSD  float64
	Attempts  int
	Turns     int
	ToolCalls int
}

// Outcome reports how a Cycle ended. Stop is never StopComplete unless every
// phase's Condition held, so a Cycle that ran out of attempts reports the
// unmet condition instead of a finished run.
type Outcome struct {
	Verdict   condition.Verdict
	Cycle     string
	Phase     string
	Stop      Stop
	Phases    []PhaseOutcome
	Changes   []tool.Change
	Ledger    Rows
	Turns     int
	ToolCalls int
	SpendUSD  float64
}

// Runner drives a Cycle's phases in order against one project.
type Runner struct {
	driver   Driver
	log      Log
	repoRoot string
}

// NewRunner builds a Runner over repoRoot. Log may be nil, in which case
// phase transitions and verdicts are reported only on the Outcome.
func NewRunner(repoRoot string, driver Driver, log Log) *Runner {
	return &Runner{repoRoot: repoRoot, driver: driver, log: log}
}

// Run drives every phase of c toward goal. It returns an error only when a
// phase's model work or its Condition could not run at all: a Condition that
// does not hold is reported through Outcome, since that is a verdict rather
// than a failure.
func (r *Runner) Run(ctx context.Context, c Cycle, goal string) (Outcome, error) {
	if err := c.Validate(); err != nil {
		return Outcome{}, err
	}

	ledger := NewLedger()
	out := Outcome{Cycle: c.Name}

	for _, phase := range c.Phases {
		out.Phase = phase.Name
		ledger.SetPhase(phase.Name)

		if err := r.logPhase(c.Name, phase.Name, "phase_start", condition.Verdict{}); err != nil {
			return Outcome{}, err
		}

		po, err := r.runPhase(ctx, c, phase, goal, ledger, &out)
		if err != nil {
			return Outcome{}, err
		}

		out.Phases = append(out.Phases, po)
		out.Turns += po.Turns
		out.ToolCalls += po.ToolCalls
		out.SpendUSD += po.SpendUSD
		out.Verdict = po.Verdict
		out.Ledger = ledger.Rows()

		if err := r.logPhase(c.Name, phase.Name, "phase_end", po.Verdict); err != nil {
			return Outcome{}, err
		}

		if !po.Verdict.Holds {
			out.Stop = StopConditionUnmet

			return out, nil
		}
	}

	out.Stop = StopComplete

	return out, nil
}

// runPhase runs one phase up to its attempt bound, stopping as soon as its
// exit Condition holds. Each attempt carries the previous verdict, since the
// reason a phase did not advance is the only thing that tells the next
// attempt what is missing.
func (r *Runner) runPhase(
	ctx context.Context, c Cycle, phase Phase, goal string, ledger *Ledger, out *Outcome,
) (PhaseOutcome, error) {
	po := PhaseOutcome{Phase: phase.Name}
	logged := ledger.Rows()

	for attempt := 1; attempt <= phase.attempts(); attempt++ {
		po.Attempts = attempt

		result, err := r.driver.Drive(ctx, Attempt{
			Cycle:  c.Name,
			Phase:  phase,
			Number: attempt,
			Prompt: Prompt(goal, phase, out.Changes, ledger.Rows(), po.Verdict),
			Ledger: ledger,
		})
		if err != nil {
			return PhaseOutcome{}, fmt.Errorf("cycle %s phase %s: %w", c.Name, phase.Name, err)
		}

		if err := r.logRows(c.Name, phase.Name, ledger.Rows(), logged); err != nil {
			return PhaseOutcome{}, err
		}

		logged = ledger.Rows()

		po.Turns += result.Turns
		po.ToolCalls += result.ToolCalls
		po.SpendUSD += result.SpendUSD
		out.Changes = mergeChanges(out.Changes, result.Changes)

		verdict, err := phase.Exit.Holds(ctx, State{
			RepoRoot:     r.repoRoot,
			Goal:         goal,
			Phase:        phase.Name,
			Changes:      out.Changes,
			Ledger:       ledger.Rows(),
			LoopComplete: result.Complete,
			LoopReason:   result.Stop.Reason,
		})
		if err != nil {
			return PhaseOutcome{}, fmt.Errorf("cycle %s phase %s: %w", c.Name, phase.Name, err)
		}

		po.Verdict = verdict

		if err := r.logVerdict(c.Name, phase.Name, attempt, verdict); err != nil {
			return PhaseOutcome{}, err
		}

		if verdict.Holds {
			return po, nil
		}
	}

	return po, nil
}

// Prompt builds what a phase's model work starts from: the standing goal,
// the phase's own instruction, the change set, and the ledger. The prior
// phase's transcript is deliberately absent. Measured over 11 real
// transcripts, 97.6% of one is content a tool can produce again, and a
// carried summary of the rest goes stale in a way a re-read cannot.
func Prompt(goal string, phase Phase, changes []tool.Change, rows Rows, previous condition.Verdict) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Standing goal\n\n%s\n\n## Phase: %s\n\n%s\n\n", goal, phase.Name, phase.Goal)
	fmt.Fprintf(&b, "This phase ends when the harness observes %q. Nothing you write about your own "+
		"progress advances it.\n\n", phase.Exit.Name())

	if len(changes) > 0 {
		b.WriteString("## Change set so far\n\n")

		for _, c := range changes {
			fmt.Fprintf(&b, "- %s (+%d/-%d)\n", c.Path, c.Added, c.Removed)
		}

		b.WriteString("\n")
	}

	if !rows.Empty() {
		b.WriteString(rows.Render())
	}

	if previous.Condition != "" && !previous.Holds {
		fmt.Fprintf(&b, "## Previous attempt\n\nThe harness refused to advance: %s\n", previous.Reason)
	}

	return b.String()
}

func (r *Runner) logPhase(cycleName, phase, kind string, verdict condition.Verdict) error {
	detail := map[string]any{detailCycle: cycleName, detailPhase: phase, "event": kind}
	text := fmt.Sprintf("%s cycle: %s %s", cycleName, phase, strings.ReplaceAll(kind, "_", " "))

	if verdict.Condition != "" {
		detail["condition"] = verdict.Condition
		detail["holds"] = verdict.Holds
		detail["reason"] = verdict.Reason
	}

	return r.append(event.Event{Kind: event.KindCycle, Text: text, Detail: detail})
}

// logRows records the ledger rows an attempt added, so a client renders
// what the phase found rather than only whether it advanced.
func (r *Runner) logRows(cycleName, phase string, now, before Rows) error {
	for _, h := range now.Hypotheses[len(before.Hypotheses):] {
		err := r.append(event.Event{
			Kind: event.KindHypothesis,
			Text: fmt.Sprintf("%s: %s (%s)", h.Cause, h.Observation, h.Verdict),
			Detail: map[string]any{
				detailCycle: cycleName, detailPhase: phase, "cause": h.Cause, "experiment": h.Experiment,
				"observation": h.Observation, "verdict": h.Verdict,
			},
		})
		if err != nil {
			return err
		}
	}

	if sweepsChanged(now, before) {
		last := now.Sweeps[len(now.Sweeps)-1]

		err := r.append(event.Event{
			Kind: event.KindHypothesis,
			Text: fmt.Sprintf("sweep %s: %d hit(s) dismissed", last.Pattern, len(last.Dismissed)),
			Detail: map[string]any{
				detailCycle: cycleName, detailPhase: phase, "pattern": last.Pattern, "language": last.Language,
				"path": last.Path, "artifact": last.Artifact, "dismissed": len(last.Dismissed),
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// sweepsChanged reports whether an attempt recorded a sweep or added a
// dismissal to one, since a re-recorded sweep merges into its predecessor
// rather than appending a row.
func sweepsChanged(now, before Rows) bool {
	if len(now.Sweeps) == 0 {
		return false
	}

	return len(now.Sweeps) != len(before.Sweeps) || dismissals(now) != dismissals(before)
}

func dismissals(r Rows) int {
	n := 0
	for _, s := range r.Sweeps {
		n += len(s.Dismissed)
	}

	return n
}

func (r *Runner) logVerdict(cycleName, phase string, attempt int, verdict condition.Verdict) error {
	return r.append(event.Event{
		Kind: event.KindCycle,
		Text: fmt.Sprintf("%s: %s", verdict.Condition, verdict.Reason),
		Detail: map[string]any{
			detailCycle: cycleName, detailPhase: phase, "event": "verdict", "attempt": attempt,
			"condition": verdict.Condition, "holds": verdict.Holds, "reason": verdict.Reason,
		},
	})
}

func (r *Runner) append(ev event.Event) error {
	if r.log == nil {
		return nil
	}

	if _, err := r.log.Append(ev); err != nil {
		return fmt.Errorf("logging cycle event: %w", err)
	}

	return nil
}
