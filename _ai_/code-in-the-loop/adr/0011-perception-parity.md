# ADR-0011: An observation contains only what a human could perceive

Date: 2026-08-02
Status: Accepted

## Context

The agent reads the DOM and the accessibility tree. A human reads rendered pixels. The gap is
free attack surface, and it is the gap the Comet attack used with white-on-white text and HTML
comments.

The techniques are well catalogued and cheap: zero opacity, one-pixel fonts, CSS clipping,
off-screen positioning, `aria-label` and `alt` payloads, `aria-hidden` mismatches, ARIA live
regions, HTML comments, JSON in script tags, and Unicode tag characters.

Sanitizers do not handle this, and the reason is structural. RAG pipelines and web-fetch agents
preserve hidden content deliberately because much of it is legitimate, and accessibility tooling
preserves it by definition. Fidelity and safety point in opposite directions.

## Decision

Observations are built by a filter that admits only content a human viewing the page could
perceive. Computed visibility, geometry intersected with ancestor clipping, position within the
document, contrast against effective background, font size floor, inert and hidden ancestor
chains, node type, and Unicode category. Deterministic, no model involved.

Accessibility metadata is carried in a separate labeled channel attached to its element, length
capped, never merged into page text, and treated as an identifier for a control rather than as
prose.

Both the naive and filtered extractions are computed, and their **divergence is journaled as a
signal in its own right**. Small divergence is normal and ignored. Divergence above a per-route
baseline learned by the crawler raises the route's hazard level. Imperceptible text containing
imperative language, URLs, or credential-shaped strings halts the step.

Response bodies are never passed through raw. Steps declare the fields they need by path, and
receive typed values.

## Consequences

The dominant hidden-content injection techniques stop working, and they stop working
mechanically rather than by detection, so a novel payload using a known hiding technique fails
the same way a known payload does.

The divergence detector needs no content analysis. Imperceptible text is anomalous whatever it
says, which is a rare thing in this space: a signal an attacker cannot make innocuous by
rewording.

Declared response extraction bounds a hostile response to one typed field, closing a channel
that DOM filtering does not touch.

The per-route baseline is what keeps false positives survivable, and it means the detector is
useless on a route the crawler has not visited.

The costs: contrast against gradients, images, and backdrop filters is approximate, and an
attacker can sit just above the threshold. Viewport choice makes perceptibility approximate at
the margins. Legitimate accessibility content gets demoted, which could hurt on sites that
carry real meaning in `aria-label` and nowhere else. And the filter is real code on the
observation path, so a bug there is a security bug.

Implementation is one in-page evaluation rather than per-element protocol calls, which matters
because per-element round-trips for computed style and geometry are the exact workload that
pushed browser-use from Playwright to raw CDP.

## Alternatives rejected

**Sanitize known injection patterns.** Pattern matching on content, defeated by rewording, and
IterInject optimizes payloads against deployed filters specifically.

**Use the accessibility tree as the observation.** The obvious choice and the tree is a
deliberate attack channel, since `aria-label` injection is a documented technique and live
regions announce content that never renders.

**Screenshots plus a vision model as the observation.** Genuinely solves perception parity by
construction, and it costs a vision call per step, which is the per-step model cost the
architecture exists to avoid. It also moves injection into images where no filter reaches.

**Classifier on extracted text.** Model judgment on attacker-controlled input, which is the
losing side of every measured result.
