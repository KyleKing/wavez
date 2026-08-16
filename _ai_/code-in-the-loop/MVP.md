# Bullet-tracer MVP

A tracer round goes the whole distance and hits the target, and it is visible the entire way.
It is not a prototype of one component. The point is that every seam in the architecture is
exercised end to end on day one, so the seams that are wrong are wrong loudly rather than at
integration time.

## The round

One goal, one site, all the way through, with everything else stubbed.

1. **Crawl** one WebArena origin read-only, producing `site.yaml`, `elements.yaml`, and `endpoints.yaml`
2. **Compile** a plan from a hand-written goal file plus that site knowledge
3. **Replay** the plan with zero model calls, and assert zero
4. **Break the site** by rewriting the DOM in-flight (rename a `data-testid`, reorder form fields)
5. **Falsify**, attribute the scope, and **recompile** rather than repair
6. **Escalate** to the repair agent for a break that recompilation cannot fix
7. **Auto-review** the proposal: envelope check, shadow replay, veto panel
8. **Commit** to git and show the diff

Steps 4 and 5 are the ones that prove the thesis. If a renamed test id is absorbed by locator
fallback with no model call, and a reordered form is absorbed by recompilation with one cheap
call, the design works. If both go straight to the repair agent, it does not, and finding that
out in week two is the entire value of building this shape first.

### The breaking mechanism

The same in-flight rewriting layer that simulates a redesign is the one the
[adversarial loop](design/10-adversarial-loop.md) uses to inject attacks. One piece of
infrastructure, two purposes, and it is worth building properly for that reason. It sits in the
egress proxy and rewrites responses on the way to the browser.

That also makes the security paths testable on day one without a hostile site: injecting
white-on-white text to trip the divergence detector is the same operation as renaming a
`data-testid`.

### Site selection

WebArena's dockerized sites, so numbers are comparable to FCPAgent, WALT, and the rest. Pick
the first target by three criteria: it has a real multi-field form, it has a GraphQL endpoint
(to exercise per-operation envelope classification rather than deferring it), and it has at
least one safe-method mutating route. The GitLab instance is the likely fit on all three and
that should be confirmed before committing to it, since a target lacking GraphQL pushes the
most novel security work past the tracer, which defeats the purpose.

Running the WebArena containers is the one thing here that needs your explicit go-ahead before
I touch it.

### What is stubbed

Everything not on the round: multi-origin tasks, the durable queue, Web Bot Auth signing, the
adversarial generator (seed corpus only, no generation), memory induction from a first agent
run (the tracer's goal file is hand-written), and trust-tier automation beyond `proposed` and
`trusted`.

### Done means

- a replayed run makes zero model calls, asserted in a test rather than observed
- a renamed selector is absorbed by locator fallback, no model call
- a reordered form is absorbed by recompilation, one tier-2 call, no page content in context
- an injected hidden-text payload trips the divergence detector before anything reaches a model
- a `GET /logout` and a batched GraphQL mutation are both denied without an active lease
- the auto-review path auto-approves a locator reorder and routes a step change to a human
- `git log` on the memory store reads as a legible history

Six of those seven are assertions a test suite makes. That is deliberate, because a demo that
requires a human to watch it is a demo that stops being run.

## Stack

TypeScript on Node, since you are comfortable with TS and Playwright's Node surface is the one
that gets features first and has the most examples in every model's weights.

| Layer | Choice | Why this and not the alternative |
| --- | --- | --- |
| Runtime | Node 22+, TypeScript | Playwright's home. Deno's permission model is genuinely attractive for this system and npm compat with Playwright's browser download and process spawning is exactly the risk not to take on the critical path |
| Browser control | Playwright, with a CDP session for the gate | Auto-waiting, role locators, and accessibility snapshots are what the healing strategy runs on. browser-use left Playwright for raw CDP over per-element round-trip latency, which this design avoids by doing perception work in one in-page evaluation |
| In-page perception | One bundled script, `Runtime.evaluate` | Collapses thousands of CDP round-trips into one call, which is the specific problem that pushed browser-use off Playwright |
| Egress proxy | Small Node HTTP CONNECT proxy | Origin allowlisting only, enforced at CONNECT before the tunnel opens, so no TLS interception is needed. mitmproxy would give body inspection at the proxy layer and costs a second language and a certificate story |
| Schemas | Zod, with YAML via a comment-preserving parser | Comments in memory files are where a human explains why a step is strange, and a parser that discards them breaks edit-then-approve |
| Memory store | git, via subprocess | Rollback, blame, and diff for free, and a human can edit a file in their editor |
| Journals | JSONL, buffered append | Line-oriented, greppable, never blocks a step |
| Local inference | Ollama, quantized 4–8B | Tier 1 reads the most attacker-influenced content, and local keeps it off the network |
| Remote inference | Anthropic SDK, Haiku 4.5 and Opus 5 | Tier 2 sees no page content, tier 3 is the rare repair path |
| Tests | Vitest, plus the replay environment | The seed attack corpus and the tracer assertions are the same suite |
| Environment | WebArena via Docker | Comparability with published numbers, and a site that can be broken without consequences |

Deferred deliberately: the durable queue (Hatchet or pg-boss when the local shape stops being
enough), Web Bot Auth signing, and any GUI. Review is `git diff` and a CLI, and building a
review interface before knowing the proposal volume is building for an unmeasured load.

## Innovation tokens

Three, per the usual budget, and the AI-era version of the argument is stronger: every token
costs twice now, once for you and once for the model helping you, because boring technology is
densely represented in the weights and novel technology is not.

**Token one, perception parity.** The human-visibility filter and the divergence detector.
Nothing found in the survey does this, it is the direct answer to content hidden from humans
and visible to agents, and the divergence signal is a detector that needs no model and cannot
be argued with by its input. Highest novelty and highest return.

**Token two, mutation-capability classification with write leases.** Per-operation
classification inside batched envelopes, deny-on-unknown, and leases scoped to origin,
operation class, and count. The GraphQL literature does this server-side and the agent
sandboxing work stops at method and domain, so the combination is new. This is what makes
read-only mean something.

**Token three, plan compilation.** Compiling plans from goals plus site knowledge and
recompiling rather than patching. It is what makes forms work, it is what keeps the repair
agent off the page most of the time, and it is what makes the auto-review envelope wide enough
to matter. It is also the most likely of the three to be wrong, which is why it is on the
tracer.

Everything else is deliberately boring: Playwright, Node, TypeScript, git, YAML, JSONL, CDP,
Zod, Vitest, Ollama, Docker.

Explicitly not spending tokens on a vector database or semantic retrieval, since lookup is
exact by origin and task identity. Not on an agent framework, since the loop is small and
specific and a framework would obscure the tier boundaries that are the actual design. Not on
a custom version control system, a review GUI, cloud browser infrastructure, or any training
or fine-tuning.

The one token worth reconsidering is Deno in place of Node. Its permission model would
constrain the harness process itself, which is a real second layer, and it is not worth
spending on the critical path while Playwright compatibility is the risk. The natural place to
revisit is the egress proxy, which has zero npm dependencies and is exactly where a process-level
network allowlist earns something.

## Sequence

**Round one, the tracer.** Crawler, plan compiler, runner, gate, the eight steps above. No
adversarial generation, seed corpus only. The output is the two numbers everything else depends
on: escalation rate and the proposal mix.

**Round two, whichever number disappointed.** If escalation rate is high, the work is in
expectation authoring and locator strategy. If the proposal mix is dominated by step edits,
[auto review](design/08-auto-review.md)'s premise is wrong and the envelope does not widen to
compensate.

**Round three, adversarial generation and hardening.** Turn on the generator, run it against
the envelope and the classifier, and treat every bypass as a permanent regression test.

**Round four, the queue boundary,** only if the local shape stops being enough.

Stopping after round one is a legitimate outcome. It answers whether the thesis holds, and a
negative answer there is worth more than three more rounds built on it.
