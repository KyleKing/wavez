# ADR-0008: Page content is data, never instruction, and controls never depend on model judgment

Date: 2026-08-02
Status: Accepted

## Context

Indirect prompt injection accounted for over 55% of observed prompt injection incidents in
2026. Brave demonstrated it against Perplexity Comet using white-on-white text and HTML
comments, driving cross-site actions including one-time-password retrieval from a
"summarize this page" request.

The measured picture on defenses is consistent. Architectural controls work: CaMeL's dual-LLM
split solved 77% of tasks against an 84% baseline while providing provable properties, FIDES
stopped all injection attempts in testing while improving task completion 16%, MELON reached
0.32% attack success at 68.72% utility, and egress allowlisting removes the primary
exfiltration channel regardless of whether injection succeeded. Probabilistic controls do not:
the "Attacker Moves Second" work bypassed twelve published defenses at over 90% success with
adaptive attacks, IterInject optimizes payloads against deployed filters, and adversarial
fine-tuning leaves a residual Anthropic itself calls meaningful risk.

Meta's Rule of Two gives the frame: at most two of processing untrusted input, accessing
sensitive systems, and changing external state.

## Decision

Page content is data. It is never treated as instruction, and no security control depends on a
model's judgment about it.

Every input to a model context carries a provenance label. Task memory and site knowledge are
trusted, observations are untrusted, and regions matching an `untrusted_content` hazard from
site knowledge are hostile by default. Spotlighting with randomized delimiters isolates
untrusted blocks, as a layer rather than as a control.

The Repair Agent cannot issue itself a write lease, widen an origin allowlist, edit policy or
harness code, or promote its own proposals. Every action it takes passes through the same gate
as the runner's, with no elevation.

Injection classifiers may run as telemetry. Nothing depends on their verdict.

## Consequences

An injection that fully succeeds still faces a gate that never read the page. The attacker
gets the agent's reasoning and not its authority, which is the only durable property available
given that injection cannot be prevented.

The Rule of Two is satisfied by the lease mechanism. During a read-only step the agent handles
untrusted input and touches sensitive systems and cannot change external state. During a
leased write step it can change external state, within a class and count declared in advance
and reviewed by a human.

Marking hostile regions during crawl is much cheaper and more reliable than detecting
injection at run time, and it depends on the crawler having actually visited the routes in
question.

The cost is that some legitimate page content genuinely is instruction. An on-screen "click
Continue to proceed" is real guidance that the agent must treat as untrusted, so it may
discount correct advice and take longer to recover.

Provenance labeling adds context overhead to every escalation prompt, and it competes with the
reasoning budget in exactly the way the budget study describes.

There is no way to verify the model actually honored the labels. This is a mitigation, not a
boundary, and the boundary is the gate.

## Alternatives rejected

**Trust page content from allowlisted origins.** Fails against user-generated content on
trustworthy sites, which is where the Comet attack lived.

**Classifier gating on injection detection.** Bypassed at over 90% success under adaptive
attack, and it creates false confidence proportional to how good the static benchmark looked.

**System prompt hardening as the control.** Overridable, and it provides assurance that is
not there.

**Full dual-LLM separation as in CaMeL.** The strongest published architectural defense, and
it costs seven points of utility and a large implementation. It is the right direction if the
escalation path grows more autonomy than this design gives it, and at present the agent is a
bounded repair mechanism rather than a driver, so the gate carries the load more cheaply.
