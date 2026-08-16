# ADR-0012: Models may veto, only checkers and humans may approve

Date: 2026-08-02
Status: Accepted
Amends: [ADR-0007](0007-quarantine-agent-authored-edits.md)

## Context

ADR-0007 quarantined every agent-authored edit behind human promotion, which is safe and does
not scale. A system that asks about every locator reordering gets ignored, and the 93% approval
rate observed under per-action prompting is what "asks a lot" converges to.

The obvious fix is a reviewing model that approves safe edits and escalates the rest. It fails
for the reason every model-in-the-loop control fails: the reviewer reads attacker-influenced
content, which hands the attacker a second attempt against a component whose job is to say yes.

The lifecycle survey's evidence points elsewhere anyway. Execution-grounded verification beats
judge-based verification, and verifier quality is often decisive in whether a skill library
helps or hurts.

## Decision

Approval comes from a deterministic checker or from a human. Models participate only as vetoes.

A **safe envelope** enumerates auto-approvable edits: locator reordering, adding a locator
strategy that resolves to the same element, statistics updates, marking an element missing,
adding a hazard, tightening an endpoint classification, adding a crawled element, and
recompiled plans whose steps differ only in resolved handles.

Outside the envelope, unconditionally to a human: any goal-file change, any origin allowlist
change, any expectation loosening, any endpoint classification loosening, any element semantics
change, any step addition, removal, or reordering, and any edit to something with human
provenance.

**Shadow replay** is the verification. An edit is auto-approvable only when a read-only replay
succeeds with it and fails without it. Write-path repairs cannot be shadow-replayed and
therefore always reach a human.

A **veto panel** of small local models runs last, each able only to object. Any objection routes
to a human.

## Consequences

Human involvement drops without a model ever granting authority. An injected veto model causes a
false rejection, which stalls a proposal and is safe.

Requiring shadow replay to fail *without* the edit is what stops the envelope filling with
edits that change nothing, which is how a library accumulates the near-duplicates that Library
Drift identifies as the retrieval-degradation stage.

The write path stays fully human, which is a hard ceiling on automation in exactly the place
mistakes are expensive.

Running the veto panel locally keeps the most attacker-influenced content off the network,
which is a privacy property beyond the cost one.

The costs: shadow replay is a browser session per proposal, cheap against human attention and
not free. The envelope is enumerated, so it is exactly as good as the enumeration, which is why
it is a primary target for the [adversarial loop](../design/10-adversarial-loop.md). And the
premise that most proposals fall inside the envelope depends on
[ADR-0010](0010-compile-plans-not-record-steps.md) shifting the mix away from step edits, which
is an argument rather than a measurement.

If the mix turns out to be dominated by step edits anyway, the envelope does not widen. Widening
it is precisely how these systems fail.

## Alternatives rejected

**A capable model reviewer that approves.** Puts model judgment in the trust-expanding path,
against every measured result on adaptive attacks.

**Auto-apply with rollback on regression.** The regression signal is the artifact the agent just
edited, so a weakened expectation shows no regression by construction.

**Human approval for everything, as in ADR-0007.** Safe, and it does not scale, and unscalable
review becomes rubber-stamped review, which is worse than an honest envelope.

**Static thresholds on a model reviewer's confidence.** Calibration on attacker-influenced input
is not a thing to rely on.
