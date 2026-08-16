# ADR-0009: Prefer site-declared WebMCP tools over induced sequences

Date: 2026-08-02
Status: Accepted, provisional

## Context

WebMCP is a proposed W3C standard letting sites declare capabilities as structured, callable
tools through a `navigator.modelContext` browser API, so an agent stops reverse-engineering
site behavior from rendered output. Chrome 146 shipped an early preview behind a flag in
February 2026, native Chrome and Edge support is targeted for the second half of 2026, and
Expedia, Booking.com, Shopify, Credit Karma, TurboTax, Redfin, Etsy, Instacart, and Target are
in origin trials. Reported token savings run as high as 89%.

WALT's result points the same direction from the other side: reverse-engineering the
site-designed automation underneath a click sequence produces something far more robust than
the sequence itself. WebMCP removes the reverse-engineering step by having the site author
declare it.

The status is genuinely provisional. This is a proposal in origin trial, not a shipped
baseline, and adoption outside the trial cohort is unknown.

## Decision

Site knowledge records WebMCP availability per origin and refreshes it on every crawl. Where a
site declares tools, memory induction prefers them over any induced locator sequence, and
declared input schemas inform the write-intent block.

Discovering a newly-available tool that supersedes an induced sequence is a first-class reason
to propose a memory edit.

A declared tool is treated as a hint about site structure, never as a security assurance. Its
traffic passes through the same mutation-capability classification as anything else.

Everything works without WebMCP. It is an accelerator on a code path that already exists.

## Consequences

Memories against adopting sites become dramatically more robust, since a declared tool does not
break when the layout changes, and the escalation rate on those origins should approach zero.

The write-intent block gets real operation names from the site rather than names invented
during induction, which makes the security contract more legible to a reviewer.

Recording availability during the transition is what lets the system benefit as adoption
spreads without a rewrite, and the transition period is where most of the next two years live.

The cost is a second induction path to build and maintain, and it will be exercised rarely at
first.

Declarations are attacker-visible surface on a compromised site. A tool appearing without a
corresponding site change is flagged rather than adopted, which is a heuristic and will
produce noise on sites that ship frequently.

Depending on a proposal in origin trial risks building toward something that changes shape.
Confining the dependency to the induction path and site knowledge, with no enforcement
resting on it, keeps the blast radius of a spec change small.

## Alternatives rejected

**Ignore WebMCP until it is a stable baseline.** Defensible, and it forgoes the largest
available robustness gain on exactly the high-value commercial sites in the trial cohort.

**Build primarily on WebMCP with DOM interaction as fallback.** Inverts the risk onto an
unratified proposal with unknown adoption, and most sites will not have it for years.

**Treat declared tools as trusted.** They are claims by the site about itself. Convenient, and
not a basis for skipping classification.
