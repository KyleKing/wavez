# ADR-0010: Compile plans from goals and site knowledge, do not record and patch steps

Date: 2026-08-02
Status: Accepted
Amends: [ADR-0001](0001-two-tier-memory.md), [ADR-0002](0002-deterministic-replay-agent-on-exception.md)

## Context

The original task memory was a recorded step list, and healing patched steps. Two problems.

A recording is a fact about one past execution, and making it the specification means every
site change is a change to the specification. Patching it accumulates edits that nobody
authored as a whole.

Forms break the model outright, and forms are most of what is worth automating. They are
conditional, multi-stage, server-validated, and reordered between releases. "Fill field three"
is wrong the moment a field is inserted, and the failure is silent: a new required field
receives a default nobody chose.

## Decision

A plan agent compiles a plan from a goal file plus site knowledge. The compiled plan is cached
and disposable, valid only against the site-knowledge hash it was compiled from.

Task state splits into a `.goal.yaml` carrying the goal, input schema, write intents, and
acceptance criteria, and a `.plan.yaml` carrying generated steps. The goal file is the reviewed
artifact and the plan file carries no authority of its own.

On falsification the response order is retry, locator fallback, recompile, targeted recrawl
then recompile, and only then repair against a live page.

Form steps carry a binding from input schema to field semantics, resolved at runtime against
the live form's accessibility tree, with unbound required fields an explicit outcome.

## Consequences

Forms work, which is the immediate reason for the change.

The repair agent that reads live pages becomes the fourth response rather than the first, so
untrusted page content reaches a model far less often. This falls out of the restructure rather
than being a security control added on purpose, which is the best kind.

Review improves sharply. A reviewer approving a two-line security contract no longer reads a
sixty-line generated step list to find it.

The auto-review envelope in [ADR-0012](0012-auto-review-asymmetry.md) becomes wide enough to
matter, because most durable edits become locator and site-knowledge changes rather than step
changes.

The costs are real. Compilation is a model call the recorded design did not need, paid on first
run and every recompile. The plan agent can compile a confidently wrong plan, which the
human-authored acceptance criteria exist to catch. And determinism weakens, since two
compilations from identical inputs should match and will not always, so recompilation is
journaled as an explicit event rather than happening silently.

Site knowledge becomes load-bearing. Crawler quality now determines task success rather than
only escalation cost. That coupling is correct because it concentrates maintenance in the one
artifact shared by every task on the origin, and it means a bad crawl is now a task-breaking
event.

## Alternatives rejected

**Keep recorded steps, improve the patcher.** Every improvement is a better way to maintain an
artifact that should be regenerated.

**Compile fresh on every run, no cache.** Maximally adaptive, and it pays a model call per run,
which is the cost the whole design exists to avoid.

**Record steps and treat the goal as documentation.** The original design. It puts the reviewed
security contract inside a generated file.

**Fully declarative goals with no compiled artifact.** An agent reasoning from the goal each
time. That is a plain agent loop, and [ADR-0002](0002-deterministic-replay-agent-on-exception.md)
covers why not.
