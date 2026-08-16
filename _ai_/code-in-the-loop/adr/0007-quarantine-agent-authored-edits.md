# ADR-0007: Agent-authored edits are quarantined, and may never weaken an expectation

Date: 2026-08-02
Status: Accepted, amended by [ADR-0012](0012-auto-review-asymmetry.md)

The quarantine and the write-time rejection rules stand unchanged. ADR-0012 adds a path out of
quarantine that does not require a human for a narrow, enumerated set of edits verified by
execution rather than by judgment, and it holds the line that no model ever grants approval.

## Context

A system that heals itself is a system that edits the artifact defining correct behavior. If
it can edit the oracle, it can make any failure disappear.

The Playwright Healer community reached this conclusion in practice. Self-healing must not
mean a red build automatically turns green, which is why the official workflow emits a
suggested patch and can mark a test skipped when it concludes the functionality is genuinely
broken, and both outcomes require review.

The security case is sharper. Zombie Agents demonstrates persistent control of self-evolving
agents through self-reinforcing injections, and the lifecycle survey names prompt injection
through admitted skills as one of eight safety surfaces specific to dynamic libraries. An
agent that writes executable memory under injection has converted a session compromise into a
durable one.

Library Drift supplies the performance case: unvalidated accumulation drives expected success
below the no-library baseline, and misleading entries degrade performance without producing
an error signal.

## Decision

Agent-authored edits are written to a quarantine path and never execute. Promotion to a
trusted tier requires a human.

Certain edits are rejected mechanically at write time rather than sent to review. An agent may
not remove a confirming evidence item, remove a falsifying evidence item, extend a timeout,
change a write intent, change an origin allowlist, change a trust tier, or modify a step
carrying human provenance.

Statistics updates that carry no semantic change (locator hit and miss counters, contribution
scores) are written directly, since they are observations rather than decisions.

Task memories enter at `proposed`, may reach `probationary` by human review, and reach
`trusted` automatically after enough successful runs, unless the write intents changed since
the last human review, in which case the tier resets.

## Consequences

An injected agent's most valuable move, writing a persistent instruction into an executable
memory, terminates in a quarantine directory. It also produces a reviewable artifact, so the
attempt is visible rather than silent.

The class of poisoning edit most likely to fool a reviewer, quietly weakening an expectation
so a failing step passes, never reaches a reviewer's judgment at all.

Human authorship is durable. An agent cannot silently revert a human's fix, which matters
because otherwise the review effort decays every time the site changes.

The cost is that healing is not automatic. A site redesign produces proposals, and the system
stays broken until someone reviews them, which is the deliberate trade and it will be
frustrating in exactly the moments it matters.

Review load scales with site churn, and a busy origin can generate more proposals than anyone
reads. Batching by origin, ordering by contribution impact, and retiring memories nobody
maintains are the pressure valves.

The mechanical rejection rules will occasionally block a correct edit. A genuinely
over-strict expectation needs a human to relax it, which is the right allocation and is
friction.

## Alternatives rejected

**Auto-apply with rollback on regression.** Attractive, and the detection signal is the thing
the agent just edited, so a weakened expectation shows no regression by construction.

**Model review of proposed edits.** Puts a model in the promotion path, giving the attacker a
second attempt at the same content with the same weakness.

**Full trust after N successes with no human ever.** Automates the security contract away.
Write intents in particular are the one artifact that must never be agent-authored, since
they are what bounds the damage of everything else.

**No write-back at all.** Fully safe and gives up the learning that makes the system worth
building.
