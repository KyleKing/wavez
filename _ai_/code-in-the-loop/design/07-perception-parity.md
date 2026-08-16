# Perception parity

## The problem

An agent reads the DOM and the accessibility tree. A human reads rendered pixels. Everything
in the gap between them is free attack surface, and it is the surface the Comet attack used.

The catalogue of ways to put text where an agent reads and a human does not:

- `color: white` on a white background, the canonical case
- `font-size: 1px`, `opacity: 0`, `transform: scale(0)`
- absolute positioning off-screen, `clip-path`, `clip: rect(0,0,0,0)`, zero-size containers with `overflow: hidden`
- `aria-label`, `aria-description`, `alt`, and `title` attributes, which are genuinely invisible to sighted users by design
- `aria-hidden` mismatches, where visible content is hidden from the tree or hidden content is exposed to it
- ARIA live regions, which announce content that never renders statically
- HTML comments and `<script type="application/json">` payloads
- content behind `display: none` that a naive `textContent` extraction still returns
- Unicode tag characters and other zero-width sequences inside otherwise innocent text
- text in `<noscript>`, `<template>`, and inert subtrees

Sanitizers do not handle this well, and there is a reason. RAG pipelines and web-fetch agents
deliberately preserve hidden content because it is often legitimate, and accessibility tooling
preserves it by definition. Fidelity and safety point in opposite directions here.

## The rule

**The agent's observation contains only what a human looking at the page could perceive.**

Not "only what is in the DOM", not "only what the accessibility tree exposes". What a person
would see.

This is a mechanical filter, not a judgment. It runs before anything reaches a model context,
it depends on no classifier, and it cannot be argued with by the content it is filtering.

### Extraction

Observation is built by one injected script evaluated in the page, returning a filtered tree.
It is a single `Runtime.evaluate` rather than per-element protocol calls, which matters
because per-element round-trips for computed style, paint order, and geometry are exactly the
workload that pushed browser-use off Playwright onto raw CDP. Doing it in-page collapses
thousands of round-trips into one.

For each candidate text node, the script computes:

| Test | Rejects |
| --- | --- |
| `checkVisibility({checkOpacity, checkVisibilityCSS})` | `display:none`, `visibility:hidden`, `content-visibility`, zero opacity |
| Bounding rect area above a floor, intersected with ancestor clips | Zero-size, clipped, and `clip-path`-erased content |
| Position within the scrollable document | Off-screen absolute positioning |
| Computed contrast against effective background, WCAG ratio floor | White-on-white and near-invisible text |
| Computed font size floor | One-pixel text |
| Ancestor chain has no `inert`, `aria-hidden`, `<template>`, `<noscript>` | Inert subtrees |
| Node type is text, not comment | HTML comments |
| Unicode category filter | Zero-width and tag characters |

Accessibility metadata (`aria-label`, `alt`, `title`) is not discarded, because an agent
genuinely needs it to identify controls. It is carried in a **separate, labeled channel**,
attached to the element it describes, capped in length, and never merged into page text. An
`aria-label` is an identifier for a control, and it is treated as one rather than as prose the
agent should read for meaning.

### The divergence signal

The script computes both the naive extraction and the parity-filtered extraction, and the
difference is journaled.

This is the part worth building. Text that is present in the DOM and imperceptible to a human
is *itself* anomalous, whatever it says. No content analysis is required, no model is
involved, and it does not care whether the payload is a known injection pattern or something
nobody has seen.

Thresholds drive behavior:

- small divergence, which every real site has (visually-hidden skip links, screen-reader labels, icon text), is journaled and ignored
- divergence above a per-route baseline learned by the crawler raises the route's hazard level and marks it `untrusted_content`
- imperceptible text containing imperative language, URLs, or credential-shaped strings halts the step and reports, because the benign explanations for that combination are thin

The per-route baseline is what makes this usable. Sites have idiosyncratic amounts of
legitimately hidden text, and a global threshold either misses attacks on verbose sites or
fires constantly on accessible ones. The crawler establishes the baseline as a site fact,
which is another thing the scraper earns its place by producing.

## Request and response bodies

The same rule extends past the DOM, because the user is right that bodies are a channel.

The agent never receives a raw response body. The gate parses responses and produces typed
observations: the endpoint id, the status, and any fields a step explicitly declared it needs,
extracted by path and type-checked. A step that wants a confirmation number declares a
JSONPath and gets a string, and does not get the JSON document that contained it.

This closes a channel that DOM filtering does not touch. A server can put anything in a
response body, agents routinely read them whole for context, and nothing a human sees
corresponds to that content at all. Declared extraction means the blast radius of a hostile
response is one typed field.

Console output, network error text, and page titles get the same treatment: length-capped,
labeled by provenance, never merged with page text.

## What this does not solve

Text that is genuinely visible and genuinely hostile. A comment field displaying
"ignore previous instructions" in plain black on white passes every test here, because a human
would see it too. That case belongs to the controls in
[ADR-0008](../adr/0008-page-content-is-data.md) and to the gate, and parity filtering makes no
claim on it.

Images containing instructions, if the observation pipeline ever includes screenshots. A
screenshot is by definition human-perceptible, so parity has nothing to say, and text rendered
into an image is invisible to every filter here. Keeping screenshots out of the default
observation path is the current answer, and it is a limitation rather than a solution.

Rendering differences between the harness's viewport and a real user's. Content perceptible at
one viewport and not another sits in a genuine grey zone, and the filter picks one viewport and
is therefore approximate.

Contrast computation against complex backgrounds (gradients, images, backdrop filters) is
approximate, and an attacker who wants to sit just above the threshold can.
