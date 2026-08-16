# Open questions

Things the design does not resolve, ordered roughly by how much they would change if
answered.

## The escalation rate is unknown, and everything depends on it

The economic argument rests on escalations being rare. If a typical origin escalates on 2% of
runs, the system is a large win. At 30% it costs more than a plain agent, because every run
pays replay plus failure plus repair.

Nobody has measured this for real sites over real time. The number varies by site churn, by
how well expectations are authored, and by how good locator fallback ordering gets. It is the
first thing a prototype should measure, and it should be measured before anything else in the
design is refined.

A related unknown: how much of the escalation rate is absorbed by locator reordering on the
fast path, which costs nothing, versus genuine agent invocation.

## Whether the proposal mix actually shifts

[Auto review](design/08-auto-review.md) reaches low human involvement only if most proposals
fall inside the safe envelope, and that depends on
[plan compilation](design/06-plan-compilation.md) shifting durable edits away from step changes
toward locator and site-knowledge changes. That is an argument, not a measurement.

If step edits dominate anyway, the envelope does not widen to compensate, and the honest
consequence is that human review load stays high. Measuring the mix is the second thing the
tracer should do after escalation rate.

## Whether the plan agent compiles wrong plans confidently

Compilation reads structured site knowledge with no live page, which is good for cost and
exposure and means the compiler never sees the thing it is planning against. A plan that is
internally coherent and wrong about the site passes every check until execution.

The human-authored acceptance criteria in the goal file are the backstop. Whether they are
sufficient, and whether people will write good ones, is unknown.

## Contrast thresholds and the attacker who sits just above them

[Perception parity](design/07-perception-parity.md) rejects text below a contrast ratio and a
font size floor. An attacker who wants to be technically perceptible and practically invisible
can sit just above both, and gradients, background images, and backdrop filters make the
contrast computation approximate anyway.

The divergence detector does not help here, because content above the threshold is admitted and
therefore not divergent. This is a genuine gap rather than a tuning problem.

## How much of memory induction can be automated

Turning a journal into a good task memory means dropping dead ends, generalizing concrete
values into inputs, and proposing evidence that is strict enough to be meaningful and loose
enough not to escalate constantly. That last balance is the hard part and it is not obvious an
LLM does it well.

If induction produces memories that need heavy human editing, the system's onboarding cost per
task may exceed what the task is worth. The fallback is hand-authoring with induction as a
draft, which is more work and probably still worth it for high-frequency tasks.

## Whether the endpoint catalog is trustworthy enough to act on

The catalog is a trusted input produced by an untrusted process. A crawler that misclassifies a
mutating endpoint as read creates a permanent hole in the security model.

The confidence floor and human review of classification changes are the current answer and
they are thin. Options worth thinking through: never letting a catalog entry override a
heuristic hit in the permissive direction (probably correct, and it limits the catalog's
value), requiring N independent observations before a read classification is actionable, or
treating catalog reads as advisory and only ever using the catalog to make classification
*stricter*.

That last option is appealing and it costs the `GET /logout` case in reverse, since it means
the catalog can only ever add denials, not remove them, and removing false denials is most of
the operational value.

## Persisted GraphQL queries

The design denies them absent a captured hash-to-document mapping or an operator allowlist
entry. Both are unsatisfying. Capture during crawl works only where the site emits the mapping,
and operator allowlisting of opaque hashes is a decision nobody can make well.

Whether large commercial sites are usable at all under this policy is an open empirical
question, and it may turn out that the honest answer is a per-site risk decision rather than a
mechanism.

## Whether a site can be tricked into looking read-only during crawl

A malicious site that behaves one way for a crawler and another during execution defeats
catalog-based classification by construction, and it is cheap to do.

Generic heuristics still apply, which is the argument for the strictness-only catalog above.
Beyond that there is no good answer, and the systems most worth attacking are the ones most
likely to be attacked this way.

## How review load actually behaves

The proposal-and-review model assumes a human reviews at a sustainable rate. A busy origin
under redesign could generate proposals faster than anyone reads them, at which point the
system either stalls or the human starts rubber-stamping, which reproduces the 93% approval
problem in a new place.

Batching by origin, ordering by contribution impact, and aggressive retirement of unmaintained
memories are the proposed valves and none of them are validated.

## Whether automatic promotion to trusted is defensible

It is the one place the system extends its own authority without a human. The guards are that
promotion requires successful runs rather than absence of failure, and that a write-intent
change resets the tier.

An argument exists for removing it entirely and requiring human promotion always. It would
raise review load and it would close the last automatic-trust path, and the right answer
probably depends on the escalation rate question above.

## Multi-origin tasks

The design assumes a task lives on one origin. Real tasks cross origins constantly: an OAuth
redirect, a payment processor, a document in cloud storage.

Origin allowlists handle the mechanics. What is unresolved is which origin's site knowledge is
authoritative at a given moment, how a task memory is partitioned across origins, and whether
a write lease should ever survive a cross-origin navigation. The current answer to the last
one is no, and that probably breaks payment flows.

## Where the operator actually runs this

The threat model assumes a machine the operator controls, and says nothing about whether that
should be their daily driver. A dedicated VM with its own network egress would materially
improve the containment story, following the pattern in Anthropic's containment writeup, and
it raises the cost of running the system at all.

## Cross-machine and cross-person memory sharing

Git makes sharing trivial, which makes the supply-chain concern from the lifecycle survey
immediate. A shared memory repository is an executable artifact from an outside party, and
nothing in the design addresses signing, trust of upstream authors, or what happens when a
shared memory's write intents differ from what the local operator would have approved.

Probably the right answer is that shared memories always enter at `proposed` regardless of
their upstream tier, and that has not been thought through.
