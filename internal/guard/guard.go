// Package guard classifies a shell command line as safe, approval-worthy, or
// refused, before it ever reaches a shell. Every check is deterministic and
// looks only at the command text and the project root: nothing here reads
// model output or filesystem state, since a decision that depended on either
// would no longer be reproducible.
package guard

import "strings"

const reasonNoMatch = "no destructive pattern matched"

// Verdict is the outcome of classifying a command.
type Verdict string

const (
	// Allow permits the command to run without a prompt.
	Allow Verdict = "allow"
	// NeedsApproval means the command may proceed only after a user (or
	// AllowAlways policy) approves it.
	NeedsApproval Verdict = "needs_approval"
	// Refuse means the command must not run, with or without approval.
	Refuse Verdict = "refuse"
)

const (
	rankAllow = iota
	rankNeedsApproval
	rankRefuse
)

func (v Verdict) rank() int {
	switch v {
	case Refuse:
		return rankRefuse
	case NeedsApproval:
		return rankNeedsApproval
	case Allow:
		return rankAllow
	default:
		return rankNeedsApproval
	}
}

// Worse reports whether v is a stricter verdict than other, so a caller
// merging several judgments keeps the strictest without knowing the order.
func (v Verdict) Worse(other Verdict) bool { return v.rank() > other.rank() }

// Result is a guard's decision on a command line: the worst verdict found
// among every fragment the line splits into, the reason it was assigned,
// and the exact fragment that triggered it.
type Result struct {
	Verdict  Verdict
	Reason   string
	Fragment string
}

// finding is Result before the whole-command reduction picks a winner.
type finding = Result

// Classify parses command as a shell command line and returns the worst
// verdict among every command it contains, whether reached through `;`,
// `&&`, `||`, a pipe, backgrounding, or command substitution. A fragment
// Classify cannot confidently parse yields NeedsApproval, never Allow: this
// guard fails closed.
func Classify(command string, env Env) Result {
	env.ProjectRoot = cleanRoot(env.ProjectRoot)

	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return Result{Verdict: Allow, Reason: "empty command", Fragment: ""}
	}
	// Checked on the raw string: a fork bomb's own `;` and `&` characters
	// would otherwise be sliced apart by sequence splitting below.
	if isForkBomb(trimmed) {
		return Result{Verdict: Refuse, Reason: "fork bomb", Fragment: trimmed}
	}

	outer, subs := extractSubstitutions(trimmed)

	var findings []finding
	for _, seq := range splitSequence(outer) {
		findings = append(findings, classifyPipeline(seq, env)...)
	}
	for _, sub := range subs {
		inner := Classify(sub, env)
		if inner.Verdict == Allow {
			continue
		}
		findings = append(findings, finding{
			Verdict:  inner.Verdict,
			Reason:   "command substitution runs: " + inner.Reason,
			Fragment: inner.Fragment,
		})
	}

	return worst(findings, trimmed)
}

func worst(findings []finding, whole string) Result {
	best := Result{Verdict: Allow, Reason: reasonNoMatch, Fragment: whole}
	for _, f := range findings {
		if f.Verdict.rank() > best.Verdict.rank() {
			best = f
		}
	}

	return best
}

func classifyPipeline(pipeline string, env Env) []finding {
	trimmed := strings.TrimSpace(pipeline)
	if trimmed == "" {
		return nil
	}
	if reason, ok := pipeToShellReason(trimmed); ok {
		return []finding{{Verdict: Refuse, Reason: reason, Fragment: trimmed}}
	}

	stages := splitPipeline(trimmed)
	findings := make([]finding, 0, len(stages))
	for _, stage := range stages {
		stage = strings.TrimSpace(stage)
		if stage == "" {
			continue
		}
		findings = append(findings, classifyCommand(stage, env))
	}

	return findings
}
