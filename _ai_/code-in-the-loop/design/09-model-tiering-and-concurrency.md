# Model tiering and concurrency

## Tiers

Roughly 70–80% of model calls in agent systems are classification, extraction, or formatting
that a quantized 7–14B model handles at equivalent quality. This system's mix is more extreme
than that, because the expensive work is concentrated in a repair path that should be rare.

| Tier | Runs on | Used for | Sees untrusted content |
| --- | --- | --- | --- |
| 0, no model | Deterministic code | Expectation matching, perception parity, mutation classification, locator fallback, envelope checking | Yes, and it does not matter |
| 1, local small | Ollama, quantized 4–8B on Apple Silicon | Falsification scope attribution, `semantic` evidence, auto-review veto panel, divergence triage | Yes |
| 2, remote cheap | Haiku 4.5 | Plan compilation from structured site knowledge, memory induction from journals | No |
| 3, remote frontier | Opus 5 | Repair against a live page, adversarial generation | Yes, rarely |

The split is not only about cost. **Tier 1 is local because it is the tier that reads
attacker-influenced content most often.** Keeping perception-parity triage and the veto panel
on-device means hostile page content from routine runs never leaves the machine. That is a
better argument for local inference than the price is.

Tier 2 gets remote frontier-adjacent capability precisely because it does *not* see page
content. Plan compilation reads structured site knowledge and a goal file, both trusted
artifacts, so sending it off-machine costs nothing in exposure.

Tier 3 is the exception path: a live page, a hostile-until-proven observation, and the
strongest available model because this is where a task is already failing. It should be rare
enough that its cost does not dominate, and if it is not, the memory should be retired rather
than repaired forever.

Model choice is configuration. The tier boundaries are the design.

## Latency budget

Tier 0 sits on the hot path of every request and every step, so it has a real budget. Rule
evaluation costs under a millisecond, and the only expensive branch is payload parsing for
envelope endpoints, which runs on a minority of requests and is cached by endpoint pattern.

Tier 1 costs tens to low hundreds of milliseconds locally, which is acceptable because it never
runs per-request. It runs per-falsification and per-proposal, both off the critical path.

Tiers 2 and 3 are seconds, and both are exceptional.

The design target: a healthy replayed run makes **zero** model calls at any tier. That is the
property the whole architecture exists to produce, and it is worth stating as a test.

## Concurrency

Three pools with different constraints.

**Browser contexts** are the scarce resource, at roughly 300–500MB each. A fixed pool with
tasks queued against it, sized to the host. This is what caps throughput and it is the number
that determines hosting cost.

**Per-origin serialization** is required for task execution, because two concurrent sessions
sharing cookies produce state neither run's expectations account for. A per-origin lock, held
for the task's duration. Different origins run in parallel freely.

**Crawling** is parallel across origins and rate-limited per origin by a token bucket carrying
the site's declared crawl delay, with a global concurrency cap protecting the host rather than
the site.

The asymmetry worth noting: crawl parallelism is bounded by politeness and task parallelism is
bounded by cookies. Neither is bounded by the models, which is a consequence of the models
being off the hot path.

### Holding a browser during a model call

The uncomfortable case. A tier 3 repair takes seconds and the browser context must stay open,
holding session state, throughout. At any real concurrency this is where the resource goes.

Mitigations, in order of preference. Make escalation rare, which the whole design already
targets. Bound escalation wall-clock hard, so a stuck repair returns the context to the pool
rather than holding it overnight. Prefer recompilation over repair, since recompilation needs
no browser at all, and this is a large practical benefit of
[plan compilation](06-plan-compilation.md) beyond correctness. Run the veto panel and shadow
replay off the task's context, batched.

What does not work is serializing the browser state and resuming later. Sessions carry
server-side state, ephemeral tokens, and timers, and restoring cookies is not restoring a
session.

## Async shape

The harness is I/O-bound throughout, and the concurrency model should reflect that rather than
adding threads.

Structured concurrency for anything fanning out, so a failed step cancels its siblings rather
than leaving orphaned work. Every awaited external call carries a timeout, since the failure
mode of a browser automation system is not crashing, it is hanging. Cancellation propagates to
CDP calls and to model calls, and a cancelled escalation must actually stop consuming tokens.

The Policy Gate is on the request hot path and is the one component where blocking is
unacceptable. Its cached-endpoint path must be synchronous and allocation-light, and its
parsing path can be async because it is already the slow branch.

Journals are append-only and written through a buffered writer, never blocking a step. Losing
the tail of a journal on a crash is acceptable, and delaying a step to fsync is not.

## Deployment

Two shapes from the same code, and the boundary between them is the task queue.

**Local.** An in-process queue, a browser pool sized to the laptop, Ollama for tier 1, and the
memory store as a local git repository. This is the development shape and it is a legitimate
production shape for one operator.

**Queued.** A durable task queue, workers holding browser pools, and the memory store as a git
remote. Durable execution is the property that matters, because browser tasks are long-running,
resumable at step boundaries, and expensive to restart. Hatchet fits (Postgres-backed, durable,
first-class TypeScript), and pg-boss or Graphile Worker are the boring alternatives if durable
execution turns out to be more than needed.

Design for the boundary now, build the local shape first. The specific things to keep clean:
task state lives in the journal rather than in memory, the browser pool is behind an interface,
the memory store is accessed through a repository abstraction rather than direct filesystem
calls, and no step assumes the process that started a task is the one that finishes it.

Tier 1 in the queued shape is the awkward part. Local inference on a worker means either
GPU-equipped workers or falling back to remote for tier 1, which loses the privacy property.
The honest answer is that the privacy argument for local inference is strongest in the local
deployment, and a hosted deployment trades it for scale. Worth stating rather than discovering
later.
