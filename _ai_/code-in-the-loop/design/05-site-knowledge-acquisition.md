# Site knowledge acquisition

The crawler exists to make escalation cheap. Everything it records is chosen because an agent
handed that fact resolves a failure in fewer turns, or because the Policy Gate can make a
better decision with it than with a generic heuristic.

It is permanently read-only. Not read-only by configuration, read-only by construction: the
crawler's session is opened by the Policy Gate with no write-lease capability at all, so
there is no code path by which a crawl issues a mutating request. A crawler that can be
talked into a write is an unattended agent with ambient authority wandering a site it does
not understand.

## Politeness

The 2026 standards shift changed the calculus here. Web Bot Auth gives agents cryptographic
identity through HTTP Message Signatures (RFC 9421), Ed25519 keys, and a `Signature-Agent`
header, backed by Cloudflare, Amazon, Akamai, and OpenAI with an IETF working group chartered
this year. Cloudflare free plans stopped auto-allowing verified bots as of 1 July 2026 in
favor of per-category opt-in across Search, Agent, and Training. Identifying honestly is now
the path that works rather than the path that gets blocked.

So the crawler signs its requests, declares the Agent category, and sets a descriptive user
agent with a contact URL. It fetches and honors `robots.txt` including `Crawl-delay`, honors
the robots.txt extension by which sites declare separate preferences for search, training,
and agent use, prefers `sitemap.xml` over link discovery, and uses conditional requests so a
refresh crawl of an unchanged site costs almost nothing.

Rate limiting is one request in flight per origin with the declared crawl delay, backing off
on 429 and honoring `Retry-After`. A `noai` or agent-disallow declaration means the origin is
not crawled and any task memory against it stays hand-authored, which is a real functional
cost accepted because ignoring a site's declared preference is not a thing to build.

Authenticated crawling is off by default and is a per-origin opt-in requiring a human-reviewed
route allowlist. Behind a login, a crawler is one misclassified control away from destroying
data, and the route allowlist rather than the read-only gate is the primary protection,
because the read-only gate cannot know that a page renders differently for an admin.

## Robot policy, resolved concretely

The awkward part is that this system does two things a site may feel differently about.
Crawling to build site knowledge is unattended bulk access, which is what `robots.txt` was
written for. Executing a task is a person using their own account through a tool, which
historically nobody considered a robot at all. Collapsing them into one policy gets one of the
two wrong.

The 2026 standards work makes the distinction expressible rather than something to argue about.
Cloudflare's category split across Search, Agent, and Training, and the `robots.txt` extension
letting sites declare per-use preferences separately, both exist because the old single signal
was carrying too much meaning. Web Bot Auth then gives the site a way to tell whether the
declaration is being honored by whoever claims to honor it.

The resolution used here:

| Activity | Policy consulted | Behavior on disallow |
| --- | --- | --- |
| Crawling for site knowledge | `robots.txt` path rules, `Crawl-delay`, and the agent or training preference declaration | Do not crawl. Site knowledge for the origin stays hand-authored or empty |
| Refresh fingerprint checks | Same as crawling, since it is the same activity at lower volume | Do not fetch |
| Task execution on the operator's own account | The site's agent-category declaration, and its terms | Warn the operator once per origin, record the decision, and let them proceed. It is their account and their relationship with the site |
| Execution-driven site knowledge updates | Inherits the task's basis, since the page was fetched anyway | Record, because refusing to remember what was already loaded helps nobody |

The line: **the system never fetches something on its own initiative against a declared
preference, and it does not stand between an operator and a site they are entitled to use.** A
disallow stops the crawler unconditionally. For task execution it produces a recorded warning
rather than a block, because a tool that refuses to let someone submit their own expense report
because of a file aimed at search indexers is solving the wrong problem.

Concretely: identify honestly with a descriptive user agent and a contact URL on every request
from either activity, sign with Web Bot Auth once the tooling is in place, declare the Agent
category rather than Search or Training, and never fall back to an anonymous or spoofed
identity when blocked. Being blocked while identifying honestly is a decision the site is
entitled to make, and it gets reported to the operator rather than routed around.

Volume discipline applies to execution as well as crawling, since the politeness argument does
not stop being true because a human authorized the task. One request in flight per origin,
declared crawl delay respected between steps where the site declares one, and backoff on 429
with `Retry-After` honored in both paths.

## What gets recorded, and why

| Artifact | Why an agent needs it |
| --- | --- |
| Route patterns with roles and auth requirements | Tells a lost agent where it is and where it should be |
| Element catalog with multiple locator strategies | Turns a healing problem into a list reorder |
| Element semantics (destructive, submits form, operation class) | Lets the gate classify a click before it becomes a request |
| Endpoint catalog with classifications | Converts the generic mutation heuristic into per-site fact |
| Auth markers and CSRF scheme | Distinguishes "logged out" from "page broken", which are handled completely differently |
| Hazards | Resolves the most common escalation causes in one turn |
| Flows (login, pagination, search) | Reusable sub-procedures shared across task memories |
| WebMCP tool declarations | Site-authored ground truth, preferred over anything induced |

### Endpoint classification during crawl

The crawler observes network traffic through the same gate the runner uses, so every request
a page makes is seen and classified. Read endpoints accumulate observations and a confidence
score. Endpoints the heuristics flag as mutation-capable are recorded with the rule that
flagged them, never exercised.

`GET /logout` is the case that justifies the whole catalog. No method-based check catches it,
and one crawl observation of the link plus an action-path heuristic records it permanently.
A generic heuristic has to be conservative about `/confirm` on every site forever, and a
catalog entry makes it a fact about this one.

For GraphQL and other envelope endpoints, the crawler records the envelope kind, whether
array batching and aliasing appear, and whether persisted queries are in use. Where the site
publishes or emits a hash-to-document mapping, capturing it during crawl is what makes
persisted queries classifiable at run time instead of a permanent denial.

### Element stability

Every element is recorded with several locator strategies, and repeated crawls test whether
each still resolves to the same element. Strategies that survive redesigns are promoted.
Accessible name and role generally win, `data-testid` wins where the site uses it
consistently, and structural CSS loses. Recording the observed stability rather than assuming
a preference order means the ordering is right for each site rather than right on average.

An element whose every strategy fails on a fresh crawl is not deleted, it is marked missing
with a timestamp. Deletion loses the history that a task memory's escalation report needs to
say "this control existed until 30 July and is now gone", which is exactly the evidence that
distinguishes a `skill`-scope repair from a `planning`-scope retirement.

## Refresh

Continuous rather than one-shot, at three cadences.

Cheap fingerprint checks run frequently: conditional requests against a handful of key routes,
and a hash over the accessibility tree of each. A changed fingerprint schedules a full recrawl
of that route and flags affected task memories for a drift review.

Full recrawls run on a slow fixed cadence and after any fingerprint change.

Execution-driven updates happen continuously and are the most valuable of the three. Every
task run confirms or contradicts site knowledge for free, since a locator that resolved during
a run is evidence and one that missed is too. Runs cover the paths that matter far more
densely than a crawl does, so the catalog stays accurate where it counts even when crawls are
infrequent.

## WebMCP changes the shape of this

WebMCP is a proposed W3C standard letting sites declare capabilities as structured callable
tools through `navigator.modelContext`. Chrome 146 shipped an early preview behind a flag in
February 2026, native Chrome and Edge support is targeted for the second half of 2026, and
Expedia, Booking.com, Shopify, Credit Karma, TurboTax, Redfin, Etsy, Instacart, and Target
are in origin trials. Reported token savings run as high as 89%.

Where a site declares WebMCP tools, they are the highest-confidence entries site knowledge can
hold, because the site author wrote them rather than a crawler inferring them. Tool
declarations should be preferred over induced locator sequences in memory induction, and the
declared input schemas give the write-intent block something precise to name.

The crawler records WebMCP availability per origin and refreshes it, because the interesting
period is the transition. Most sites will not have it, some will have it for part of their
surface, and a system that checks and falls back gets the benefit as adoption spreads without
a rewrite. Detecting a newly-available tool that supersedes an induced sequence is a
first-class reason to propose a memory edit.

Two cautions. A declared tool is a claim by the site about its own behavior, so it is
convenient rather than trustworthy, and a tool named `search_expenses` still passes through
the mutation-capability gate on its actual traffic. And tool declarations are attacker-visible
surface on a compromised site, so a declaration that appears without a corresponding site
change is worth flagging rather than adopting silently.
