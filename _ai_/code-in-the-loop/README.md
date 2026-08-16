# code-in-the-loop

Design notes for a browser automation system that runs learned interaction sequences as
deterministic code and calls an agent only when reality diverges from what the code
expected.

The shape of the idea: **site knowledge** is a broad per-origin model of routes, elements,
flows, endpoints, and hazards, built by a well-behaved crawler. A **plan agent** compiles a
goal plus that knowledge into an executable plan, cached as a disposable artifact. The harness
replays the plan at machine speed with no model in the loop, and every step carries explicit
expectations a parser can check. When an expectation is falsified, the response ladder is
retry, locator fallback, recompile, recrawl and recompile, and only then an agent reading the
live page. Whatever is learned is written back, versioned in git, and reviewable as a plain
diff.

Two positions carry most of the weight. Deny-by-default on anything capable of mutating state,
classified per operation inside batched request envelopes and gated by short-lived write leases.
And an observation that contains only what a human looking at the page could perceive, which
closes the channel that hides instructions from people and shows them to agents.

## Documents

Research

- [2026 prior art](research/2026-prior-art.md) — what shipped and what published in the last eighteen months, and which parts of this design are already solved elsewhere
- [Threat model](research/threat-model.md) — adversarial analysis, attacker capabilities, and what each control actually buys

Design

- [Architecture](design/00-architecture.md) — components, run lifecycle, and the fast-path / escalation split
- [Data structures](design/01-data-structures.md) — task memory and site knowledge schemas
- [Execution harness](design/02-execution-harness.md) — replay, expectation checking, escalation, and write-back
- [Security guardrails](design/03-security-guardrails.md) — the mutation-capability deny heuristic, write leases, and enforcement layers
- [Human in the loop](design/04-human-in-the-loop.md) — trust tiers, review workflow, and drift governance
- [Site knowledge acquisition](design/05-site-knowledge-acquisition.md) — crawler behavior, robot policy resolution, and what gets recorded
- [Plan compilation](design/06-plan-compilation.md) — goals compile to plans, forms bind at runtime, recompile before repairing
- [Perception parity](design/07-perception-parity.md) — observations contain only what a human could see, and divergence is the signal
- [Auto review](design/08-auto-review.md) — the safe envelope, shadow replay, and why models may only veto
- [Model tiering and concurrency](design/09-model-tiering-and-concurrency.md) — four tiers, local inference for untrusted content, browser pools and deployment
- [Adversarial loop](design/10-adversarial-loop.md) — a red team as a component, and its output as regression tests

Build

- [Bullet-tracer MVP](MVP.md) — the end-to-end round, stack selection, and where the three innovation tokens go

Decisions

- [ADR index](adr/README.md)

Loose ends

- [Open questions](OPEN-QUESTIONS.md)

## Status

Research artifact. Nothing here is implemented. The documents are written to be implementable,
so schemas and algorithms are concrete, and no code has been validated against a real site. The
two numbers the whole design rests on, escalation rate and the mix of proposed edits, are
unmeasured, and [MVP.md](MVP.md) exists to measure them before anything else is refined.
