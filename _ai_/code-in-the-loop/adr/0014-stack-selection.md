# ADR-0014: TypeScript on Node, three innovation tokens

Date: 2026-08-02
Status: Accepted
Amends: [ADR-0003](0003-playwright-with-cdp-enforcement.md)

## Context

ADR-0003 assumed Playwright plus CDP without naming a language. Two things since then decide it.

browser-use published their move from Playwright to raw CDP. Their reasons are specific: a
second network hop through the Node Playwright server websocket, which matters when making
thousands of CDP calls per step to check element position, opacity, paint order, event
listeners, and ARIA properties, plus state synchronization across browser, relay process, and
client, with documented hangs and a reliable crash on full-page screenshots above 16,000px.

That workload is close to what [perception parity](../design/07-perception-parity.md) needs, so
it is a direct hit on this design rather than someone else's problem.

Innovation tokens cost double in 2026, once for the person and once for the model assisting
them, because boring technology is densely represented in training data and novel technology is
not.

## Decision

TypeScript on Node 22+, with Playwright for actions and a CDP session for the gate.

Perception parity runs as **one bundled in-page script evaluated through `Runtime.evaluate`**,
returning a filtered tree. This is what makes Playwright survivable: the thousands of
round-trips browser-use describes collapse into one call, because the geometry, style, and
contrast work happens in the page rather than across the protocol.

The egress proxy is a small Node HTTP CONNECT proxy doing origin allowlisting at CONNECT time,
before the tunnel opens, so no TLS interception is required. Body classification stays in the
CDP layer, where plaintext is already available.

Three innovation tokens: perception parity, mutation-capability classification with write
leases, and plan compilation. Everything else boring.

## Consequences

Playwright's auto-waiting, role locators, and accessibility snapshots stay available, and the
healing strategy depends on all three. Giving them up for raw CDP would cost more than the
latency does.

The in-page evaluation pattern is the load-bearing decision. If perception parity ever needs
per-element protocol calls, browser-use's argument applies in full and the choice should be
revisited.

Dropping TLS interception from the proxy simplifies it to roughly a hundred lines with no
certificate story, at the cost of not inspecting credentials on allowed origins. That is the
Anthropic exfiltration case, and it is a real residual, listed in the threat model.

One language across harness, proxy, and tests. mitmproxy would give proxy-layer body inspection
and costs a second language and a certificate installation.

Binding to Chromium remains, which the WebMCP timeline makes cheaper than it would otherwise be.

## Alternatives rejected

**Deno.** Genuinely attractive: its permission model would constrain the harness process itself,
giving a network allowlist independent of both CDP and the proxy, plus built-in TypeScript,
formatter, linter, and test runner. Not taken because npm compatibility with Playwright's
browser download and process spawning is exactly the risk not to put on the critical path while
the thesis is unproven. The place to revisit is the egress proxy, which has zero npm
dependencies and is where a process-level network allowlist earns the most.

**Python.** Matches the surrounding ecosystem and would share a language with mitmproxy. Not
taken because Playwright's Python bindings add the relay hop browser-use identified without the
Node ecosystem's compensating maturity, and because the proxy no longer needs mitmproxy.

**Raw CDP, no Playwright.** browser-use's conclusion, correct for an agent doing per-element
extraction on every step. This design does that work in-page, so the tradeoff inverts.

**mitmproxy for egress.** Body inspection at the proxy layer, and a second language plus a
certificate story for a capability the CDP layer already provides.

**A durable queue in the tracer.** Hatchet or pg-boss are the right answers eventually, and
building distribution before measuring the local shape is building for an unmeasured load.
