# ADR-0013: Four tiers, and the tier that reads untrusted content runs locally

Date: 2026-08-02
Status: Accepted

## Context

Most model calls in agent systems are classification, extraction, or formatting that a
quantized 7–14B model handles at equivalent quality, and rule-based routing costs under a
millisecond while heavier classifiers cost 50–100ms against inference times measured in
hundreds of milliseconds.

This system's mix is more extreme, because the architecture already pushes the expensive
reasoning into a repair path that should be rare.

There is a second axis nobody routes on, and it matters more here than cost. Different
components see attacker-influenced content at very different rates, and sending that content
to a remote provider is a disclosure decision separate from a spending one.

## Decision

Four tiers, routed by capability need and by exposure to untrusted content.

Tier 0 is deterministic code: expectation matching, perception parity, mutation classification,
locator fallback, envelope checking. No model.

Tier 1 is a local quantized 4–8B via Ollama: falsification scope attribution, `semantic`
evidence, the auto-review veto panel, divergence triage. **Local because this is the tier that
reads untrusted page content most often**, so hostile content from routine runs never leaves the
machine.

Tier 2 is remote cheap: plan compilation and memory induction. Remote is acceptable precisely
because these read structured site knowledge, goal files, and journals rather than live pages.

Tier 3 is remote frontier: repair against a live page, and adversarial generation. The
exception path.

The design target is that a healthy replayed run makes zero model calls at any tier, asserted
as a test.

## Consequences

Cost tracks escalation rate rather than task volume, which is the same quantity everything else
in the design tracks.

The routing rule is legible: capability need on one axis, untrusted exposure on the other, and
tier 1 is where they cross.

Local inference for tier 1 is a privacy property and not only a cost one, and it is the better
argument. It also removes a per-call network dependency from the components that run most often.

Tier 2 gets good models cheaply because it sees no page content, which is a direct benefit of
[ADR-0010](0010-compile-plans-not-record-steps.md) moving compilation off the live page.

The costs: local inference is a hardware dependency and a quality ceiling, and a 4–8B model
attributing falsification scope will be wrong sometimes, which shows up as a wrong recovery
strategy rather than as an error. Tier boundaries are also a place for capability creep, where
a task quietly gets promoted to tier 3 because it is easier than making tier 1 work.

The sharpest cost appears on deployment. In a queued deployment, local tier 1 means GPU-equipped
workers or falling back to remote, which loses the privacy property. The honest position is that
the property is strongest in the local shape and a hosted deployment trades it away, which is
worth saying now rather than discovering during a migration.

## Alternatives rejected

**One frontier model everywhere.** Simplest, and it pays frontier prices for scope attribution
and sends every page observation off-machine.

**One local model everywhere.** Cheapest and most private, and plan compilation and repair are
genuinely hard reasoning where a small model produces confidently wrong plans.

**Route purely on cost or difficulty.** The standard approach, and it ignores the exposure axis,
which means the component reading the most hostile content gets routed remote whenever it is
cheap to do so.

**Fine-tune a local model for the guardrail tasks.** Attractive later and it spends an
innovation token on training infrastructure for components that mostly should not be models at
all.
