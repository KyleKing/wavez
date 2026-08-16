# ADR-0004: Deny any request not provably incapable of mutating state

Date: 2026-08-02
Status: Accepted

## Context

A read-only mode that keys on HTTP method is trivially wrong. `GET /logout` destroys a
session. `GET /confirm?token=…` completes an action. `POST /search` mutates nothing. Method
is a hint about intent, not a statement about capability.

Batching makes it worse. A single HTTP request can execute hundreds of operations through
GraphQL aliasing or array batching, so a control counting requests sees one thing while the
server does five hundred. The same shape recurs in JSON-RPC batches, tRPC batch links, and
OData `$batch`.

The GraphQL spec supplies one firm invariant: resolution of fields other than top-level
mutation fields must be side-effect-free and idempotent, so only top-level fields of a
`mutation` operation may have side effects. That is checkable, provided the document text is
present, which persisted queries deliberately prevent.

## Decision

A request is admitted only when it can be shown incapable of mutating state. Classification
runs on method, URL, headers, and parsed payload, per operation for enveloped requests, with
the request's verdict taken as the maximum severity across operations. Anything unresolvable
is denied.

Method-safe requests are still classified as mutation-capable when they carry action-shaped
path segments, method-override parameters or headers, single-use tokens in the query, a body
on a bodiless method, a navigation carrying a CSRF token, or a match against an endpoint the
catalog classified as mutating.

Requests classified as mutation-capable are admitted only against an active write lease
scoped to origin, operation class, and remaining count, issued from a write intent declared
in the task memory and reviewed by a human.

## Consequences

Read-only mode means something enforceable. During a step with no lease, no mutating request
leaves the browser, and this holds even if the agent driving the step is fully under attacker
control.

A successful injection is bounded by the lease in scope. Full control of the agent's reasoning
during a step holding `expense.create` with count 1 yields one expense, not an emptied
account.

Aliasing cannot smuggle a fan-out past a count-based lease, because aliases decrement the
lease individually.

False denials are the accepted cost, and they are real. A legitimate task hitting an
unclassifiable operation stalls until a human resolves it. The mitigation is that a denial is
a reviewable event carrying the request and the classification path, and resolving one writes
a permanent classification into the endpoint catalog. False denials are self-liquidating and
false admissions are not, which is the asymmetry the whole posture rests on.

Persisted GraphQL queries are the sharpest edge. Common on exactly the large commercial sites
a user most wants automated, and genuinely unresolvable at the gate. Capturing hash-to-document
mappings during crawl handles some cases, and the rest require an operator explicitly
allowlisting a hash, which is a human accepting a specific risk rather than the system
silently degrading.

Payload parsing per request has a latency cost, small next to network round trips and not
zero, and it means the gate must implement a GraphQL parser and several envelope formats.
That is real code with real bugs in a security-critical position.

## Alternatives rejected

**Method-based read-only.** The obvious approach, and wrong for `GET /logout`, which is not
an exotic case.

**Blocklist known-dangerous endpoints.** Fails open on anything not yet enumerated, which
is every new site.

**Model classification of requests.** Puts the control on the wrong side of the injection
boundary. The "Attacker Moves Second" result, twelve published defenses bypassed at over 90%
success by adaptive attacks, is the general argument.

**Allow and audit.** Lowest friction, and it detects the damage after it happens. For
irreversible operations the audit log is a record of the loss.

**Partial admission, stripping mutating operations from a batch.** Rewriting a payload
changes semantics the page depends on, and produces state neither the site nor the
expectations account for.
