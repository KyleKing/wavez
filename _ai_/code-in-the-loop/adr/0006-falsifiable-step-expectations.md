# ADR-0006: Every step carries confirming and falsifying evidence

Date: 2026-08-02
Status: Accepted

## Context

Deterministic replay needs a definition of "this step worked" that a parser can evaluate,
because the alternative is a model call per step and that is the cost the design exists to
avoid.

A timeout alone is a poor oracle. It is slow, since a failed step pays the full timeout before
anyone notices, and it is blind, since a page that loaded successfully into the wrong state
satisfies any timeout.

FCPAgent addresses exactly this. Its Falsifiable Commitment Units carry a subgoal, an optional
linked skill, confirming evidence at precondition, progress, and completion stages, falsifying
evidence organized into execution drift, skill mismatch, and planning failure, and a
confidence score. It reports 13.8% relative improvement on WebArena with gains concentrated in
long-horizon tasks, including 161% on shopping tasks of eleven or more steps.

## Decision

Each step carries a `confirm` set evaluated as a conjunction and a `falsify` set evaluated as
a disjunction, both polled in parallel until one resolves or `timeout_ms` elapses. Each
falsifying item carries an `attribute` naming its diagnostic scope: precondition, execution,
skill, or planning.

Evidence kinds are parser-evaluable by default. A `semantic` kind exists for genuinely
unstructured confirmations and is the exception.

Escalation attributes before it repairs, and the attributed scope determines the response,
from re-locating an element up to recommending the memory be retired.

## Consequences

Failures are detected as soon as they are visible rather than after a timeout, which is where
most of the latency saving over fixed-wait automation comes from.

The oracle is meaningful. A step that loaded the wrong page fails, where a timeout-based check
would pass.

Attribution stops the healer from doing damage. A healer that answers every failure by finding
a new selector will attach a task to whatever button is nearby when the truth is that the
feature was removed, and scope attribution turns that case into a retirement recommendation.

Authoring cost is the price. Every step needs evidence written, and a memory whose evidence is
weak passes when it should not. Induction proposes evidence from what was actually observed,
which is a start and is not a substitute for review.

Over-strict evidence causes spurious escalation, and the failure mode is worse than it sounds,
because a memory that escalates constantly costs more than no memory at all.

The `semantic` escape hatch will be over-used unless authoring guidance pushes against it,
since it is much easier to write than a precise structural check.

## Alternatives rejected

**Timeout only.** Simple, slow, and blind to wrong-state success.

**Screenshot comparison.** Catches visual regressions and is defeated by every dynamic
element on the page. The GUI agent failure-mode study covers the ways visual memory misleads.

**Model verification per step.** Semantically strong, and it reintroduces per-step cost and
per-step injection surface, which is the thing being avoided.

**Confirming evidence only.** Half the value. Falsifying evidence is what turns a slow timeout
into an immediate stop, and the attribute field on it is what makes scope-aware repair
possible.
