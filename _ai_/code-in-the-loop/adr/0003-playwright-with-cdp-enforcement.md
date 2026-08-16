# ADR-0003: Playwright for actions, CDP and a proxy for enforcement

Date: 2026-08-02
Status: Accepted, amended by [ADR-0014](0014-stack-selection.md)

ADR-0014 fixes the language as TypeScript on Node, replaces the mitmproxy egress layer with a
CONNECT-time origin allowlist needing no TLS interception, and records the constraint that keeps
Playwright viable: perception work runs as one in-page evaluation, never as per-element protocol
calls.

## Context

Playwright gives good ergonomics for the action layer: role-based locators, auto-waiting,
accessibility-tree snapshots, and a mature Python and TypeScript surface. Its request
interception is convenient and it is an abstraction, and abstractions leak in the places
where a security control most needs not to.

The Chrome DevTools Protocol gives direct access to `Fetch.requestPaused` with per-resource-type
patterns, `Network.setBlockedURLs`, and the browser-level switches that disable service
workers and WebRTC. It also carries known gaps, documented in the Chromium tracker, around
document requests after the first navigation and around `resourceType: Fetch` patterns.

## Decision

Playwright drives actions. CDP, accessed through Playwright's CDP session, implements the
network gate and channel reduction. An out-of-process HTTP proxy enforces the origin
allowlist independently.

The runner never touches the network directly. Both the runner and the crawler open sessions
through the Policy Gate, and the gate owns the CDP session.

## Consequences

The action layer keeps Playwright's ergonomics, which matters because the element catalog's
locator strategies map onto Playwright locators directly and role-based locators are what
survive redesigns.

The enforcement layer operates below the abstraction, so a Playwright version change cannot
silently alter interception semantics.

Three independent layers means a gap in any one is not a bypass. The CDP gaps are the
specific reason the proxy exists, and the proxy exists on the assumption that some channel
nobody enumerated will eventually escape CDP.

The cost is real. Two interception mechanisms means two places to get wrong, and a request
denied by the proxy but not by CDP produces a confusing failure that needs correlation in the
journal to diagnose.

Binding to CDP means binding to Chromium. Firefox and WebKit are out, and the WebMCP timeline
(Chrome and Edge in the second half of 2026) makes that less costly than it would otherwise
be.

The proxy adds a process to operate and a TLS interception setup to get right, which is
meaningful complexity for a system that would otherwise be a single process.

## Alternatives rejected

**Playwright routing alone.** One mechanism, much simpler, and it puts the security control
inside the abstraction it is meant to constrain, with no independent layer when the
abstraction has a gap.

**CDP alone, no Playwright.** Browser Harness takes this route. It removes the abstraction
mismatch and gives up auto-waiting, role-based locators, and accessibility snapshots, all of
which the healing strategy depends on.

**A browser extension.** Better access to some page-level events and it runs in the same
process it is policing, which is the wrong side of the boundary.

**Proxy only, no in-browser gate.** Sufficient for origin allowlisting and blind to the
action layer, so a destructive click that mutates client-side state before any request is
generated goes unseen.
