# Prior art through mid-2026

What follows is the state of the field as of August 2026, organized by the part of the
proposed design each body of work touches. The short version: every individual piece of
this system exists somewhere, and the combination does not. The interesting work is in the
seams, particularly between a deterministic replay layer and an agent that is allowed to
edit the thing being replayed.

## Deterministic replay of learned web procedures

The strongest confirmation that the task-memory idea is sound comes from **WALT (Web Agents
that Learn Tools**, ICLR 2026). WALT reverse-engineers functionality a website already
implements into deterministic, callable tools, spanning discovery (search, filter, sort),
communication (post, comment, upvote), and content management (create, edit, delete). An
agent calls `search(query)` instead of reasoning its way through a sequence of clicks. The
paper's own framing of the problem matches the motivation here: step-by-step UI interaction
plus heavy LLM reasoning breaks under dynamic layouts and long horizons. WALT reports higher
success with fewer steps and less LLM-dependent reasoning.

The important divergence: WALT induces *site-level* tools, which is closer to the site
knowledge half of this design than the task memory half. A task memory composes several such
tools plus navigation and data entry into one user-meaningful outcome. Both layers are
worth having, and WALT is good evidence that the site-level one can be induced automatically
rather than hand-written.

**Agent Workflow Memory** (ICLR) extracts reusable workflows from successful trajectories
and lets the agent induce increasingly complex workflows on the fly, offline from training
data or online during evaluation. **MemP** builds procedural memory from step-level and
script-level trajectory summaries. **WebXSkill** and the broader skill-learning line cover
the same ground with different induction strategies.

The cautionary counterweight is **"Are Online Skill and Memory Modules Always Worth Their
Tokens?"**, a budget-constrained study finding that skill and memory modules increase token
usage without proportional gains, and that under a fixed budget the context they consume
competes directly with the reasoning they were meant to support. Simpler architectures
sometimes win. This is a direct argument for the design choice made here: task memories are
executed as code on the fast path and consume zero model tokens, and site knowledge is
loaded into a model context only after an escalation. Memory that is executed is cheap;
memory that is merely *retrieved into a prompt* is not.

## Detecting that something went wrong

**Falsifiable Commitment Planning (FCPAgent)** is the closest published match to the
"expectation plus timeout, then intervene" mechanism described in the brief, and it is
worth copying almost directly. It treats each plan step as a testable hypothesis. Each
Falsifiable Commitment Unit carries five fields: a subgoal, an optional linked skill from a
reusable library, confirming evidence (precondition, progress, and completion signals),
falsifying evidence organized hierarchically into execution drift, skill mismatch, and
planning failure, and a planner confidence score.

Two details matter for the design here. First, the hybrid testing strategy: cheap
evidence-matching screens routine transitions and an LLM verifier is invoked only for
ambiguous or high-stakes situations, which is exactly the cost profile wanted. Second,
scope-aware repair: because falsification is attributed to execution, skill, or planning
scope, the repair revises only the contradicted component and preserves valid progress
rather than restarting the task.

Results were a 13.8% relative improvement on WebArena (65.3% against 57.4%), with the gains
concentrated in long-horizon tasks, 161% relative improvement on shopping tasks of eleven
or more steps. Long tasks are where a naive agent silently goes off-track, and long tasks
are precisely what a task memory is for.

**Naive Visual Memory is Not Enough** studies GUI agent failure modes and is a useful list
of the ways a replayed trajectory can be wrong in ways a screenshot comparison will not
catch.

## Self-healing, as shipped

Playwright v1.56 shipped three agents (Planner, Generator, and Healer) with an
`npx playwright init-agents --loop=healer` setup that writes markdown agent instructions
and an `.mcp.json` pointing at the Playwright MCP server. The Healer watches a run, and on
failure inspects the current DOM through MCP snapshots, identifies the root cause,
generates a corrected interaction using role-based locators, and re-runs.

The single most important lesson from the QA community's reaction is a governance one:
self-healing must not mean a red build automatically turns green. The official Healer
workflow emits a *suggested patch*, and it can also mark a test skipped when it concludes
the functionality is genuinely broken. Both outcomes require human review. A healer that
silently rewrites its own oracle has deleted the signal it existed to produce. This is the
direct ancestor of the trust-tier and proposal-branch model in
[human-in-the-loop](../design/04-human-in-the-loop.md).

Elsewhere, `robotframework-selfhealing-agents` repairs broken locators via LLM with
expansion to other failure modes planned, and vendor tooling (Keysight, BrowserStack, and
others) has moved past pure locator patching toward natural-language element description
resolved by intent and user-visible behavior rather than technical identifiers. **PALADIN**
covers self-correcting agents for tool-failure cases more generally.

**Browser Harness** is worth naming as the aggressive end of the spectrum: an open-source
CDP-based harness whose stated philosophy is that when the agent hits a missing capability
it edits the harness code in real time and adds the function it needs, without human
intervention. That is the design this one deliberately does not adopt. Self-modifying
harness code is an enormous unreviewed attack surface, and the whole point of separating
data (task memories, site knowledge) from code (the harness) is that the data can be
diffed, reviewed, and rolled back while the code stays fixed.

## Skill libraries decay, and the decay is measurable

Two 2026 papers make the maintenance problem concrete and both should shape the governance
design.

**Library Drift** defines the failure formally: expected pass@1 with the accumulated
library falls *below* the no-skill baseline. Three stages compound. Skills accumulate
without outcome validation, retrieval precision degrades as near-duplicates and stale
entries crowd out useful ones, and misleading skills actively harm without producing an
explicit error signal. The diagnostics are cheap and directly portable: a per-skill
contribution score of `(successes − failures) / trials`, structured attribution verdicts
labeling each failure as helped, hurt, neutral, or inapplicable, and router engagement
metrics tracking what fraction of tasks receive an injection at all.

The proposed governance is a three-part "ratchet": outcome-driven retirement once a skill
has enough evidence (100 trials) and a negative contribution (≤ −0.10), a hard active-cap
(50) forcing eviction of the lowest contributor, and a meta-skill authoring prior
constraining the synthesizer's output style. The ablations are the useful part. Removing
the authoring prior cost 43% of the gains, and *harsher* retirement dropped performance
below baseline. Governance applied without a sufficient evidence threshold does more damage
than no governance.

**Skill Drift Is Contract Violation** frames staleness as a broken contract between the
skill and the external services, packages, APIs, and configurations it references, which
is the right frame for a browser system where the "external service" is a website that
redesigns without warning.

**Dynamic Agent Skills: A Lifecycle Survey** gives an eight-stage lifecycle (evidence
acquisition, proposal, verification and admission, organization, retrieval and composition,
maintenance and repair, distillation, and governance) and a seven-field skill record:
applicability, executable policy, termination conditions, reusable interface, edit field,
verification handle, and lineage. The schema in
[data structures](../design/01-data-structures.md) is a browser-specific instantiation of
that record. Two survey findings drive design decisions here: flat retrieval degrades at
moderate library sizes (tens to hundreds of entries), and focused libraries outperform
comprehensive ones. Both argue for partitioning memories by origin and retrieving by exact
task identity rather than semantic similarity over one global pool.

The survey also flags eight safety surfaces specific to dynamic skills, including prompt
injection *through admitted skills* and supply-chain poisoning. A learned task memory is an
attacker-reachable artifact.

## Security

### The attack surface is real and demonstrated

Brave's security team demonstrated indirect prompt injection against Perplexity Comet, with
adversarial instructions hidden in elements invisible to a human reader (white-on-white
text, HTML comments) causing the browser agent to perform sensitive cross-site actions,
including fetching one-time passwords and reaching a banking portal, in response to nothing
more than "summarize this page." Indirect injection accounted for over 55% of observed
prompt injection incidents in 2026, making it the dominant real-world vector.

**IterInject** shows feedback-guided iterative optimization of indirect injections, which
matters because it means payloads adapt to whatever filter is deployed. **Zombie Agents**
demonstrates persistent control of self-evolving agents through self-reinforcing
injections, an attack aimed squarely at systems that write their own memory back to disk.
That is this system, and it is the reason agent-authored memory edits land in quarantine
rather than in the trusted set. **Silent Egress** covers implicit injection that leaks data
without leaving a trace.

The underlying structural problem predates LLMs. A browser agent operating in a session
with live cookies is a confused deputy in the classical CSRF sense. The browser holds
ambient authority and attaches it to any request for that origin regardless of what
initiated the request, so the server cannot distinguish an agent following a hostile
instruction from the user acting deliberately. Any control that lives only in the model's
judgment is on the wrong side of that boundary.

### What actually works

The 2026 consensus, and it is fairly firm: injection cannot be solved inside current LLM
architectures, so the goal is blast radius reduction through controls that sit outside the
model's probabilistic behavior.

Defenses with measured results:

- **CaMeL**, a dual-LLM split separating untrusted content processing from action control, solved 77% of tasks against an 84% baseline, buying deterministic security properties for a seven-point utility cost
- **FIDES**, information-flow control tracking confidentiality and integrity labels through operations, stopped all injection attacks in testing and *improved* task completion 16%, apparently through the structural clarity the labeling imposed
- **MELON**, execution monitoring that compares a normal run against a masked re-execution, reached 0.32% attack success at 68.72% utility, though it only catches tool-call-based attacks
- **Meta's Rule of Two**, a capability constraint holding that an agent may have at most two of: processing untrusted input, accessing sensitive systems, and changing external state
- **Egress allowlisting**, deterministic blocking of requests to non-approved domains, which removes the primary exfiltration channel whether or not the injection succeeded

Defenses that fail under adaptive attack:

- Adversarial fine-tuning alone, where even Claude Opus 4.5's published ~1% attack success rate is described by Anthropic as meaningful risk
- System prompt instructions telling the model to ignore embedded instructions
- Classifier screening in isolation, where the "Attacker Moves Second" work bypassed twelve published defenses at over 90% success with adaptive attacks, despite those defenses reporting low residual rates against static ones

The pattern is unambiguous. Anything evaluated by a model can be argued with. Anything
evaluated by a parser cannot.

**ceLLMate** is the most directly applicable system: a proxy architecture that intercepts
and validates browser agent actions across both channels that matter, network requests and
DOM manipulation, using declarative allowlists of domains, actions, and data types rather
than blocklists. Its network filter examines target URL, method and parameters, and cookie
and credential transmission. Its DOM filter restricts form submission targets, element
modification, credential handling, and script execution. The two-channel structure is
adopted directly in [security guardrails](../design/03-security-guardrails.md), extended
with the payload-level operation classification the brief asked for.

**Building Browser Agents** (Vardanyan) covers the architectural hygiene: process
isolation, network segmentation, capability restriction, resource quotas, least privilege,
an explicit read-only versus write distinction, time-bound elevation, and comprehensive
audit of privileged actions.

### Approval fatigue is a measured failure, not a hypothetical

Anthropic's containment writeup supplies the numbers that should govern the human-in-the-loop
design. Under per-action prompting in Claude Code, users approved roughly 93% of permission
prompts, which is a rubber stamp rather than a control. Auto mode replaced most prompts with
model-based classifiers and catches roughly 83% of overeager behaviors before execution,
while OS-level sandboxing plus the human-in-the-loop model cut prompt volume 84%.

The same document contains a useful incident: an egress allowlist was bypassed because
`api.anthropic.com` was on it, and attackers could exfiltrate through it using
attacker-controlled API keys. The fix was a defensive man-in-the-middle proxy inside the VM
inspecting traffic to their own API and blocking unauthorized credentials. Domain
allowlisting alone is insufficient when an allowed domain is itself a general-purpose data
sink.

Claude in Chrome's shipped model is "ask before acting" versus "act without asking", plus
unconditional confirmation for a fixed high-risk set (publishing, purchasing, deleting,
downloading, entering sensitive information), plus category-level blocks on finance, adult,
and pirated content. Commentators consistently identify approval fatigue as the mechanism
by which users defeat the control themselves.

### Batching defeats per-request reasoning

The GraphQL security literature makes the case the brief anticipated. A single HTTP request
can execute hundreds of operations through aliasing or array batching, so any control
counting HTTP requests sees one thing while the resolver does five hundred. The
spec-compliant invariant that helps: resolution of fields other than top-level mutation
fields must be side-effect-free and idempotent, so only top-level fields of a `mutation`
operation are allowed to cause side effects. That gives a parser something firm to check,
provided the document text is actually available, which persisted queries deliberately
prevent. Existing mitigations (`graphql-armor-max-aliases`, `graphql-armor-max-tokens`,
`graphql-no-batched-queries`) are server-side, so a client-side gate has to reimplement the
parsing.

## The standards layer is moving

Two efforts change what this system can rely on within its lifetime.

**WebMCP** is a proposed W3C standard letting sites declare capabilities as structured,
callable tools through a `navigator.modelContext` browser API, so an agent stops
reverse-engineering behavior from pixels. Chrome 146 shipped an early preview in February
2026 behind a flag, with native Chrome and Edge support targeted for the second half of
2026, and Expedia, Booking.com, Shopify, Credit Karma, TurboTax, Redfin, Etsy, Instacart,
and Target in origin trials. Reported token savings run as high as 89%.

For this design, a WebMCP tool declaration is the highest-confidence entry that can appear
in site knowledge, because the site author wrote it. It should be preferred over any induced
locator sequence, and its presence or absence per origin should be recorded and refreshed.

**Web Bot Auth** gives agents cryptographic identity through HTTP Message Signatures
(RFC 9421), Ed25519 keys, and a `Signature-Agent` header, backed by Cloudflare, Amazon,
Akamai, and OpenAI with an IETF working group chartered in 2026. It is an identity layer
rather than an access policy, so a verified agent can still be blocked or charged. As of
1 July 2026 Cloudflare free plans stopped auto-allowing verified bots in favor of
per-category opt-in (Search, Agent, Training), and a robots.txt extension lets sites
declare per-use preferences separately.

The consequence for the crawler is that identifying honestly is now the path of least
resistance rather than a liability, and that a site's declared agent policy is a first-class
input to what the crawler is permitted to record.

## Where this design differs from everything above

Three choices are not standard practice in the surveyed work.

The fast path spends no model tokens at all. Most memory and skill systems retrieve prior
experience *into a prompt*. Here a task memory is executable and the model is absent from a
successful run, which is what makes the budget study's warning survivable.

The agent is a repair mechanism rather than a driver. It is invoked on falsification, with
a bounded budget, against a specific failing step, which keeps injected page content out of
the loop entirely on the common path. An agent that never reads the page cannot be injected
by it.

Mutation capability is classified at the payload layer, not the method layer. Nothing found
in the survey does per-operation classification of batched envelopes on the client side as a
precondition for allowing a request out of the browser. The GraphQL work is server-side and
the agent-sandboxing work stops at method and domain.

## Sources

- [WALT: Web Agents that Learn Tools](https://arxiv.org/abs/2510.01524) ([ICLR 2026 poster](https://iclr.cc/virtual/2026/poster/10008481))
- [Agent Workflow Memory](https://openreview.net/forum?id=NTAhi2JEEE)
- [WebXSkill: Skill Learning for Autonomous Web Agents](https://arxiv.org/html/2604.13318v1)
- [Are Online Skill and Memory Modules Always Worth Their Tokens?](https://arxiv.org/pdf/2606.15017)
- [Falsifiable Commitment Planning for Self-Correcting Web Agents](https://arxiv.org/html/2607.24167v1)
- [Naive Visual Memory is Not Enough: A Failure-Mode Study of GUI Agents](https://arxiv.org/pdf/2606.14106)
- [Playwright Test Agents: Planner, Generator, and Healer](https://qaskills.sh/blog/playwright-test-agents-planner-generator-healer)
- [Playwright Healer Agent and Self-Healing Tests in 2026](https://qaskills.sh/blog/playwright-healer-agent-self-healing-tests)
- [Playwright AI Ecosystem 2026](https://testdino.com/blog/playwright-ai-ecosystem)
- [2026 Self-Healing Test Automation: Beyond Locator Patching](https://www.keysight.com/blogs/en/tech/software-testing/2026-self-healing-test-automation-beyond-locator-patching)
- [robotframework-selfhealing-agents](https://github.com/MarketSquare/robotframework-selfhealing-agents)
- [PALADIN: Self-Correcting Language Model Agents to Cure Tool-Failure Cases](https://arxiv.org/pdf/2509.25238)
- [Browser Harness](https://jimmysong.io/ai/browser-harness/)
- [Library Drift: Diagnosing and Fixing a Silent Failure Mode in Self-Evolving LLM Skill Libraries](https://arxiv.org/html/2605.19576v1)
- [Skill Drift Is Contract Violation](https://arxiv.org/html/2605.10990)
- [Dynamic Agent Skills: A Lifecycle Survey and Taxonomy of Evolving Skill Libraries](https://arxiv.org/html/2607.10113v1)
- [Indirect Prompt Injection: Attacks, Defenses, and the 2026 State of the Art](https://zylos.ai/research/2026-04-12-indirect-prompt-injection-defenses-agents-untrusted-content/)
- [IterInject: Indirect Prompt Injection Against LLM Agents via Feedback-Guided Iterative Optimization](https://arxiv.org/pdf/2605.24659)
- [Zombie Agents: Persistent Control of Self-Evolving LLM Agents via Self-Reinforcing Injections](https://arxiv.org/pdf/2602.15654)
- [Silent Egress: When Implicit Prompt Injection Makes LLM Agents Leak Without a Trace](https://arxiv.org/pdf/2602.22450)
- [ceLLMate: Sandboxing Browser AI Agents](https://arxiv.org/pdf/2512.12594)
- [Building Browser Agents: Architecture, Security, and Practical Solutions](https://arxiv.org/pdf/2511.19477)
- [Prompt Injection Attacks in LLM and AI Agent Systems: A Comprehensive Review](https://www.mdpi.com/2078-2489/17/1/54)
- [How we contain Claude](https://www.anthropic.com/engineering/how-we-contain-claude)
- [Use Claude in Chrome safely](https://support.claude.com/en/articles/12902428-use-claude-in-chrome-safely)
- [Claude in Chrome: A Threat Analysis](https://labs.zenity.io/p/claude-in-chrome-a-threat-analysis)
- [Preventing GraphQL batching attacks](https://dev.to/ivandotv/preventing-graphql-batching-attacks-56o3)
- [GraphQL Batching Abuse and How to Stop It](https://bipi.in/blog/graphql-batching-abuse-mitigation)
- [GraphQL Security 2026 — Beyond Introspection](https://ringsafe.in/graphql-security-beyond-introspection/)
- [GraphQL mutations specification](https://graphql.org/learn/mutations/)
- [Cross-Site Request Forgery](https://words.filippo.io/csrf/)
- [WebMCP: How Websites Will Expose Tools to AI Agents](https://zuplo.com/blog/what-is-webmcp)
- [The State of WebMCP: July 2026](https://www.spronta.com/blog/state-of-webmcp-july-2026/)
- [Chrome's WebMCP and native agent APIs](https://agentmarketcap.ai/blog/2026/04/07/chrome-firefox-native-agent-apis-2026-browser-agentic-primitives)
- [Web Bot Auth Explained: Cloudflare and IETF's New Standard](https://stellagent.ai/insights/web-bot-auth-cloudflare-ietf)
- [Web Bot Auth in 2026](https://www.coronium.io/blog/web-bot-auth-verifiable-ai-agents-2026)
- [Chrome DevTools Protocol: Fetch domain](https://chromedevtools.github.io/devtools-protocol/tot/Fetch/)
- [Chrome DevTools Protocol: Network domain](https://chromedevtools.github.io/devtools-protocol/tot/Network/)
