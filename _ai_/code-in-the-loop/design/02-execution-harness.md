# Execution harness

## Replay

The runner loads a task memory, checks preconditions, opens a browser session through the
Policy Gate with the memory's origin allowlist, and walks the steps in order. Each step is a
propose-act-observe-verify cycle.

Locator resolution walks the element's `locators` list in order of observed reliability, and
records a hit or miss for whichever strategy resolved. This is the cheapest possible healing,
it runs on the fast path, and it costs nothing. A step whose primary CSS locator has decayed
and whose role-based locator still resolves does not escalate at all, it just quietly reorders
its own preference list. Over time the list sorts itself toward whatever the site keeps
stable, which is usually accessible names.

Waiting is expectation-driven rather than time-driven. The runner does not sleep, it polls the
confirming evidence set until one matches or the step's `timeout_ms` elapses, and it polls the
falsifying set in parallel so that a failure is detected as soon as it is visible rather than
after the full timeout. A visible error banner ends the step immediately, which is where most
of the latency saving over a fixed-timeout approach comes from.

## Expectation checking

Evidence kinds and how each is evaluated:

| Kind | Source | Cost |
| --- | --- | --- |
| `url` | Current page URL | Free |
| `element_visible` | Locator resolution against the accessibility tree | Cheap |
| `element_text`, `element_value` | Resolved element property | Cheap |
| `network` | Gate journal for the current step, matched by endpoint id and status | Free, already recorded |
| `console` | Console messages captured during the step | Free |
| `download` | Download events | Free |
| `semantic` | LLM judgment over a page snapshot | Expensive, avoid |

Everything except `semantic` is a parser check, and a well-authored memory uses only those.
`semantic` exists because some confirmations genuinely are unstructured (an emailed receipt
rendered as prose, a status conveyed only by color), and it is the escape hatch rather than
the norm. FCPAgent's hybrid testing is the model to copy: cheap evidence matching screens
routine transitions and model verification is reserved for ambiguous or high-stakes cases,
which is what keeps the token cost near zero on a healthy run.

A step passes when all confirming evidence matches and no falsifying evidence matches. The
asymmetry is intentional. Confirmation is a conjunction because a partially-loaded page can
match one signal while the operation failed, and falsification is a disjunction because any
single sign of trouble is enough to stop.

## Escalation

When a step falsifies or times out, the runner attributes before it repairs. The `attribute`
field on the matching falsifying evidence gives the scope directly, and a timeout with no
falsifying match is attributed by a fixed rule set (element not found means execution scope,
page structure unrecognized means skill scope, expected route absent from site knowledge means
planning scope).

| Scope | Meaning | Response |
| --- | --- | --- |
| `precondition` | The run should not have started, or state was lost (session expired, wrong route) | Re-establish preconditions if a known flow exists, otherwise halt |
| `execution` | The step is right, the interaction failed (locator decayed, transient error, timing) | Repair Agent may re-locate and retry within budget |
| `skill` | This step is wrong for the current site (control moved, flow reordered) | Repair Agent proposes a step edit, run halts pending review unless the memory is probationary |
| `planning` | The approach is stale (feature removed, flow replaced) | Halt, flag the memory for retirement, do not attempt to heal |

The scope-aware split is what stops the healer from doing damage. A healer that responds to
every failure by finding a new selector will happily attach a task to whatever button happens
to be nearby when the real answer is that the feature no longer exists. Attributing first
turns that into a retirement recommendation instead of a plausible-looking wrong memory.

### What the agent receives

The escalation payload is assembled by the harness, and the agent has no ability to request
more:

- the task memory's intent and the failing step, marked trusted
- the preceding steps' outcomes from the journal, marked trusted
- the site-knowledge slice for the current route: elements on this route, hazards, flows, and relevant endpoints, marked trusted
- the observation, meaning the accessibility snapshot, the URL, and the gate decisions for this step, marked untrusted
- any page region matching an `untrusted_content` hazard, marked hostile
- a budget: maximum actions, maximum model calls, wall-clock ceiling

The site-knowledge slice is the reason escalation usually resolves in one or two turns. An
agent told "the list on this route virtualizes and off-screen rows are absent from the DOM"
does not spend six turns discovering it.

### Budget and give-up

Escalation is bounded, and exhausting the budget is a normal outcome rather than an error.
The run halts, the journal records everything attempted, and the operator gets a report. A
repair loop with no ceiling is how a stuck agent burns a token budget overnight, and the
budget-constrained study's finding that augmentation costs frequently exceed their gains
applies with full force to a repair path that is allowed to run indefinitely.

## Write-back

A successful repair produces two artifacts.

A **site knowledge edit** when the problem was the site: a reordered locator list, a new
locator strategy, a new element, a newly observed endpoint classification, a new hazard. These
are low-risk, they are shared across every task on the origin, and locator reordering that
happened on the fast path is written directly since it is a statistics update rather than a
semantic change.

A **task memory edit** when the problem was the task: a changed step, a new step, a changed
expectation. These are higher-risk and always land in quarantine.

Both carry provenance recording the agent, the journal entry that justified the change, and
the version superseded. Neither is trusted on write. The promotion path is in
[human-in-the-loop](04-human-in-the-loop.md).

One rule constrains all agent-authored edits: **an agent may never weaken an expectation.**
Removing a confirming evidence item, removing a falsifying item, or extending a timeout are
edits that make a memory pass more easily, which is precisely how a self-healing system
converts a real failure into a green run. Such edits are rejected at write time rather than
sent to review, and the agent is told to propose a step change instead. This is the
Playwright Healer community's lesson stated as a mechanical rule: self-healing must not mean
a red run automatically turns green.

## Memory induction

A task with no memory runs as a full agent session, with the gate active and every write
requiring explicit human approval, since there are no declared write intents to authorize
against. The journal from that session is the raw material for induction.

Induction is a separate, offline step rather than something that happens at the end of the
run. It reads the journal, drops the exploratory dead ends, generalizes the concrete values
that varied into declared inputs, proposes confirming and falsifying evidence for each
retained step from what was actually observed, and emits a `proposed`-tier memory alongside
its declared write intents.

Doing this offline matters. A memory induced in the same session that produced it inherits
whatever confusion the session had, and a session that ended with a human approving three
write prompts should produce a memory whose write intents a human reads carefully before it
ever runs unattended.

The generalization step is where induction is hardest and where WALT's approach is
instructive. Rather than generalizing a click sequence, look for the site-designed automation
underneath it, since a form submission is a create operation and a filter control is a query
parameter. A memory expressed in terms of the site's own operations survives redesign far
better than one expressed as coordinates in a flow. Where WebMCP is available, this stops
being inference: the site declares its tools, and a memory should reference them in
preference to any induced sequence.

## Concurrency and idempotency

Runs against the same origin share a browser profile and are serialized, because two
concurrent sessions sharing cookies produce state neither run's expectations account for.
Runs against different origins are independent.

A write step that halts on ambiguous confirmation leaves the task in an unknown state, and the
harness records this explicitly rather than reporting failure. Whether the write landed is
genuinely unknown, and reporting it as failed invites a human to retry a duplicate. Where a
site supports an idempotency key, the `writes` block carries it and the step becomes safely
retryable, which is worth recording in site knowledge as a per-endpoint property.
