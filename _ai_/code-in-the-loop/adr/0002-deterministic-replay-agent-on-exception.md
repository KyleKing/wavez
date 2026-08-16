# ADR-0002: Replay deterministically, invoke the agent only on falsification

Date: 2026-08-02
Status: Accepted, amended by [ADR-0010](0010-compile-plans-not-record-steps.md)

Zero model calls on a healthy run still holds. ADR-0010 inserts recompilation between
falsification and repair, so the escalation ladder is longer and the live-page repair agent is
reached less often. The first run of a novel task now costs a compilation rather than a full
agent session, which softens the amortization problem described below.

## Context

The default architecture for browser automation in 2026 is an agent loop: observe the page,
reason, act, repeat. It is adaptable and it pays full model cost and full latency on every
step of every run, including the thousandth run of a task whose pages have not changed.

The budget-constrained study of web agents found that skill and memory modules frequently
fail to earn their token cost, because the context they consume competes with the reasoning
they were meant to support. That finding applies to memory *retrieved into a prompt*. It does
not apply to memory that is executed.

## Decision

A task memory is executable. The runner replays it through Playwright with no model in the
loop. Each step's outcome is checked against parser-evaluable expectations. The agent is
invoked only when an expectation is falsified or a step times out, with a bounded budget and
a single failing step to address.

## Consequences

A successful run costs the wall-clock time of the browser actions and zero tokens. This is
the whole point, and it makes running a task hundreds of times economically different from
running it once.

Injection exposure becomes proportional to escalation rate rather than to task volume,
because on a clean run no page content reaches a model context. Every improvement in replay
reliability is also a security improvement, which is an unusually well-aligned incentive.

Latency is bounded by the site rather than by inference, and expectation-driven waiting means
a visible error ends a step immediately instead of after a fixed timeout.

The first run of a novel task has no memory and is a full agent session at full cost with the
full injection surface. The system is an amortization strategy and is worth nothing for
one-shot tasks.

A site under active redesign escalates constantly and the fast path degrades to *slower* than
a plain agent, since every run pays replay plus failure plus repair. Detecting sustained
escalation and retiring the memory is necessary, and without it the system's worst case is
worse than the thing it replaces.

Expectations can be wrong permissively. A step whose confirming evidence is too weak passes
while the task silently goes off the rails, and no amount of determinism helps.

## Alternatives rejected

**Agent-driven with memory as prompt context.** The mainstream approach. It keeps full
adaptability and pays full cost forever, and the budget study suggests the memory often does
not pay for itself.

**Fully deterministic, no agent at all.** Classical browser automation, which is exactly the
brittleness that motivates the field.

**Agent verifies each step.** A model call per step to confirm the outcome. It catches
semantic failures a parser misses and reintroduces per-step cost and per-step injection
surface. FCPAgent's hybrid testing is the compromise adopted instead: cheap evidence matching
by default, model verification only for genuinely ambiguous cases.
