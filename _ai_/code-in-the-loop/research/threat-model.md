# Threat model

## Scope

The system under analysis is a browser automation harness that replays learned interaction
sequences, escalates to an LLM on failure, and writes what it learns back to a versioned
store. It runs on an operator's machine or a machine they control, against sites where the
operator holds credentials.

The starting assumption, taken from the 2026 literature rather than assumed for
convenience: prompt injection cannot be solved inside current LLM architectures. Injection
*will* succeed sometimes, and the design question is what an attacker gets when it does.

## Assets

| Asset | Why an attacker wants it |
| --- | --- |
| Live authenticated sessions | Ambient authority to act as the operator on every allowlisted origin |
| Credentials in the profile | Durable access surviving the session |
| Data readable through those sessions | Direct exfiltration target |
| The memory store | Persistent, reusable control over future runs |
| The harness's network position | A vantage point inside the operator's network boundary |

The memory store is the asset that distinguishes this system from a plain browser agent. A
successful injection against a stateless agent ends with the session, and a successful
injection that reaches the store persists and re-executes. Zombie Agents demonstrates
precisely this against self-evolving agents, and it is the reason agent-authored edits land
in quarantine.

## Actors

**Hostile page content.** The dominant vector. Text placed on a page an agent reads, by any
party who can write there. Brave demonstrated this against Perplexity Comet with instructions
hidden as white-on-white text and HTML comments, causing cross-site actions including fetching
one-time passwords and reaching a banking portal, triggered by nothing more than "summarize
this page." Indirect injection accounted for over 55% of observed prompt injection incidents
in 2026. This actor needs no relationship with the operator and no compromise of the target
site, only the ability to place text where the agent will read it, which a comment field or a
product review provides.

**A compromised or malicious target site.** Full control over page content, network responses,
and any WebMCP tool declarations. Strictly more capable than the above.

**A third-party subresource.** Analytics, ad, and CDN origins loaded by an otherwise trustworthy
site, which is the supply chain the site itself does not fully control.

**An attacker with write access to the memory store.** Through a compromised machine, a
compromised git remote, or a poisoned upstream if memories are ever shared.

**The operator, as a source of error.** Approving something they did not read. Not adversarial,
and empirically the most likely path to damage, given the 93% approval rate observed under
per-action prompting.

## Attack paths and what stops them

### Injected instruction during escalation

An attacker places instructions on a page. The step falsifies, the Repair Agent is invoked, it
reads the page, and it follows the injected instruction.

This is assumed to succeed. Nothing in the design prevents the agent from being convinced.

What bounds it: the agent's actions pass through the Policy Gate with no elevation, it holds
no write lease, and the origin allowlist is per-task. An agent fully under attacker control
can read within the allowlist and can propose a memory edit that lands in quarantine. It
cannot write to the site, cannot reach an origin outside the allowlist, and cannot promote its
own proposal.

The residual: reading within the allowlist can itself exfiltrate through URL structure if any
allowlisted origin is attacker-influenced. Allowlist scope is the only control, and this is
the same shape as Anthropic's incident where an allowlisted domain became the exfiltration
channel. The mitigation is that the allowlist is per-task and reviewed, so it contains the
origins the task needs rather than a standing set.

### Injection reaching the memory store

The injected agent proposes a memory edit that embeds instructions, weakens an expectation, or
redirects a step to an attacker-chosen target. It re-executes on every future run.

What stops it: proposed edits are quarantined and never execute. Promotion requires a human.
Expectation weakening is rejected mechanically at write time rather than sent to review, so
the most useful class of poisoning edit never reaches a reviewer's judgment at all. Write
intents cannot be edited by an agent, so a poisoned memory cannot grant itself write
capability.

The residual: slow drift, where many individually-reasonable edits sum to something harmful.
Periodic provenance review of the cumulative diff since the last human-verified version is the
countermeasure, and it is manual and imperfect.

### Injection during the fast path

An attacker wants to influence a run that never escalates.

There is no path. On a clean run no page content enters a model context, because there is no
model. This is the strongest property in the design, and it is a consequence of the
performance decision rather than a security feature added on purpose. Injection exposure is
proportional to escalation rate, so a stable site drives it toward zero and every improvement
in replay reliability is also a security improvement.

### Confused deputy through ambient authority

The browser attaches session cookies to any request for an origin regardless of what initiated
it, so the server cannot distinguish an agent following a hostile instruction from the operator
acting deliberately. This is classical CSRF, and it predates LLMs entirely.

What stops it: read-only by default, so no mutating request leaves the browser without an
active lease. Leases are scoped to origin, operation class, and count, and expire with the
step. A dedicated profile carries cookies only for allowlisted origins. No password manager,
no autofill, no stored payment methods.

The residual: within a granted lease, the gate verifies the operation class and count rather
than the operation's intent. A lease for `expense.create` with count 1 permits one expense
creation, whatever its contents.

### Batched operation smuggling

An attacker induces one request carrying many operations, so a per-request control sees one
thing while the server does hundreds. GraphQL aliasing and array batching are the standard
mechanisms, and the same shape appears in JSON-RPC batches, tRPC batch links, and OData
`$batch`.

What stops it: per-operation classification with the request's verdict taken as the maximum
severity across operations, alias counting so a fan-out decrements the lease per alias, and
denial of any envelope where an operation cannot be classified.

The residual: persisted queries, where only a hash is transmitted. Unresolvable at the gate,
therefore denied, unless the hash was captured during crawl or explicitly allowlisted by an
operator accepting the risk.

### Uninterceptable channels

Service workers, WebSocket frames, WebRTC data channels, `sendBeacon`, `fetch` with
`keepalive`, and background sync all evade or complicate CDP `Fetch` interception, and the
Chromium tracker carries known gaps in `Fetch.requestPaused` around document requests and
certain resource-type patterns.

What stops it: the channels are removed rather than filtered. Service workers disabled, WebRTC
disabled, background sync off. An out-of-process proxy enforces the origin allowlist
independently of anything happening inside the browser.

The residual: a channel nobody enumerated. The proxy is the answer, and it is the reason the
egress layer exists as a separate process rather than as more CDP code.

### Malicious WebMCP declaration

A compromised site declares a tool whose name and description misrepresent what it does, so an
agent selects it believing it is safe.

What stops it: declarations are convenient rather than trustworthy. A tool's actual traffic
passes through the same classification as any other request, and a declared `search_expenses`
that issues a mutation is denied on the wire. Declarations appearing without a corresponding
site change are flagged rather than adopted.

### Operator approval fatigue

The operator clicks approve without reading, at something close to the observed 93% rate.

What stops it: most enforcement is not a prompt. Gate denials stop rather than ask. Contracts
are approved once at review time rather than per action. Security-relevant diffs are visually
distinct and cannot be batch-approved.

The residual: this is the most likely path to real damage in the whole model, and no
mechanism fully closes it. Keeping prompt volume low so that each prompt carries weight is the
entire strategy.

## Defenses considered and rejected

**Model-based injection classifiers as a primary control.** The "Attacker Moves Second" work
bypassed twelve published defenses at over 90% success with adaptive attacks, despite low
reported rates against static ones. IterInject demonstrates feedback-guided optimization of
injections against deployed filters. A classifier may run as telemetry, and nothing depends
on it.

**System prompt hardening as a control.** Instructions telling the model to ignore embedded
instructions are overridable and provide false assurance.

**Adversarial fine-tuning as sufficient.** Even Claude Opus 4.5's published ~1% attack success
rate is described by Anthropic as meaningful risk, and 1% of many runs is not small.

**Agent-editable harness code.** Browser Harness treats real-time self-modification as a
feature. It makes the reviewable surface unbounded and puts enforcement code inside the blast
radius of an injection.

**Semantic diffing of proposed memory edits by a model.** Attractive for review ergonomics,
and it puts a model in the promotion path where the attacker gets a second attempt at the
same content. Review reads the raw diff.

## Where this model is weakest

Analysis rather than measurement. Nothing here has been tested against a real adversary, and
the classification heuristics in particular are the kind of thing that looks complete until
someone spends an afternoon on it.

The endpoint catalog is a trusted input built by an untrusted process. A crawler that
misclassifies a mutating endpoint as read creates a permanent hole, and the confidence floor
and human review of classification changes are the only controls. This deserves more thought
than it has had.

Nothing addresses a malicious site that behaves correctly during crawl and differently during
execution, which is cheap to do and defeats catalog-based classification by construction.
The generic heuristics still apply, which is the argument for never letting a catalog entry
override a heuristic hit in the permissive direction.
