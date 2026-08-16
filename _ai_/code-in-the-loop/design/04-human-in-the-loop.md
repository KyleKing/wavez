# Human in the loop

## The number to design against

Under per-action permission prompting in Claude Code, users approved roughly 93% of prompts.
A control a human clicks through nineteen times out of twenty is not a control, it is a
latency tax with a compliance story attached. Every commentary on Claude in Chrome's
"ask before acting" mode identifies the same failure: approval fatigue drives users to the
permissive setting, which defeats the mechanism entirely.

The response that worked was moving oversight off the per-action path. Auto mode replaced most
prompts with model-based classifiers and catches roughly 83% of overeager behaviors before
execution, and OS-level sandboxing plus the retained human-in-the-loop model cut prompt volume
by 84%.

The lesson generalizes into three rules used throughout this design.

Approve *contracts*, not actions. A human reviews a task memory's declared write intents once,
and the gate enforces them on every subsequent run without asking again. The reviewable
artifact is a diff a person reads carefully at a calm moment rather than a modal interrupting
a task.

Make the boundary absolute where it can be. A denial from the mutation-capability gate is not
a prompt. It stops. Prompting is reserved for cases where a human genuinely has information
the system does not.

Spend the interruptions on what matters. Prompt volume is a budget, and every routine prompt
spent is attention unavailable for the one that counts.

## Trust tiers

Every task memory and every element entry carries a tier.

| Tier | How it runs | How it leaves the tier |
| --- | --- | --- |
| `proposed` | Never runs unattended. Executes only in a human-observed session, and every write requires explicit approval | Human review and promotion |
| `probationary` | Runs unattended for read-only tasks. Any step carrying a write intent pauses for approval. Falsification halts rather than escalates | Automatic after N successful runs with no `skill`-scope falsification, provided the contribution score clears the floor |
| `trusted` | Runs unattended. Write leases issue against declared intents without prompting. Falsification escalates to the Repair Agent | Demoted on sustained failure, or by a human |

Automatic promotion from `probationary` to `trusted` is the one place the system extends its
own authority without a human, and it is bounded deliberately. Promotion requires successful
runs, not merely absence of failure, and it never applies to a memory whose write intents
changed since the last human review. A write intent edit always resets the tier to `proposed`,
because the write intent block is the security contract and nothing else in the file carries
comparable consequence.

Demotion is automatic and aggressive in the other direction, since the asymmetry is correct:
being slow to trust costs latency and being slow to distrust costs damage.

## Review surface

Review is a git diff, and the design decisions that make it work are all about signal density.

Statistics churn is separated from semantics. Locator hit and miss counters update on every
run, so they live in `elements.yaml` and a reviewer looking at a policy change never wades
through counter increments to find it.

Every proposed edit carries its justification inline. The provenance block names the journal
entry that motivated the change, and the review surface renders the relevant journal excerpt
beside the diff: what was expected, what was observed, what the agent concluded. A reviewer
should never have to reconstruct why an edit was proposed.

Edits that change the security contract are visually distinct and cannot be batch-approved.
Changes to `write_intents`, origin allowlists, and endpoint classifications are a different
class of decision from a selector update.

The reviewer's actions are approve, reject, edit-then-approve, and retire. Edit-then-approve
is the important one and the reason YAML rather than a database. A human who sees the agent
almost got it right should fix the two lines and accept, and that edit is recorded with human
provenance so the memory reads as human-verified rather than agent-authored.

Human edits are never overwritten by an agent. When a step carries human provenance and a
later agent proposes changing it, the proposal is flagged as contradicting a human decision
and requires review regardless of the memory's tier.

## Drift governance

Skill libraries decay measurably, and Library Drift defines the endpoint precisely: expected
success with the accumulated library falls below the no-library baseline. Three mechanisms
prevent it, and all three are ported here.

**Contribution scoring.** Each memory tracks `(successes − failures) / runs`. Each element
locator tracks hits and misses. Both are already recorded for other reasons, so the
diagnostic is free.

**Outcome-driven retirement with an evidence floor.** A memory is proposed for retirement when
its contribution drops below a threshold *and* it has enough runs for the score to mean
anything. The evidence floor is the part that gets skipped and should not be. The Library
Drift ablations showed harsher retirement dropping performance below baseline, meaning
governance applied without sufficient evidence does more damage than no governance. Retirement
is a proposal to a human rather than an automatic deletion, because a memory failing because
a site is down for a day should not be destroyed.

**Bounded active set per origin.** Retrieval precision degrades as near-duplicates and stale
entries crowd out useful ones, so each origin has a cap on active memories and exceeding it
forces a merge-or-retire decision. In practice this system is less exposed than a general
skill library, because retrieval is exact lookup by task identity rather than similarity
search, so a stale memory does not get injected into an unrelated task. The cap is about
review load rather than retrieval precision.

**Attribution verdicts.** Each escalation records whether the memory helped, hurt, or was
irrelevant to the outcome. A memory whose escalations are consistently attributed to
`planning` scope is stale in a way no amount of healing fixes, and that pattern is the
retirement signal worth trusting most.

## Periodic re-review

Two scheduled reviews, both manual, both cheap enough to actually happen.

A **drift review** per origin, triggered when the site's fingerprint changes materially or on
a fixed cadence, walks the memories that escalated since the last review.

A **provenance review** per memory, walking the cumulative diff from the last human-verified
version to the current one. This is the countermeasure to slow-drift poisoning, where an
attacker influences many individually-reasonable edits that together move a memory somewhere
harmful. Each edit passed review on its own and the sum did not, so the sum has to be reviewed
as a sum. The Zombie Agents work on self-reinforcing injections in self-evolving agents is the
reason to treat this as expected rather than paranoid.

## Denials as a review queue

Every gate denial is journaled with the request, the classification path, and the reason, and
denials accumulate into a queue a human works through. Resolving one usually means writing a
classification into the endpoint catalog, which converts a recurring stall into a permanent
fact about the site.

This is what makes deny-by-default survivable. The false-denial rate starts high on a new
origin and decays as the catalog fills, and the work of resolving them produces a durable
artifact rather than a click. False admissions have no equivalent feedback loop, which is
the asymmetry the whole posture rests on.
