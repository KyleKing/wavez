# Design proposal: a single-user coding agent, optimized relentlessly for one person

Date: 2026-08-12. Built on the research in [docs/research/](research/), synthesized in [research/SYNTHESIS.md](research/SYNTHESIS.md). This document is the design, not the research; it argues for specific choices rather than surveying options.

## Thesis

Every open-source coding agent surveyed (Crush, OpenCode, Goose, Cline, Aider) is trending toward more surfaces: desktop apps, IDE extensions, web dashboards, multi-tenant config, enterprise policy layers. Claude Code carries the same shape, for the same reason: it serves a fleet of users with different trust levels, different orgs, different compliance needs. None of that is your problem. You are the only user, you already trust yourself, and there is no fleet to manage.

That difference isn't cosmetic. It's the entire design lever. Every piece of surface area built to answer "whose policy wins" or "how do we audit this for a team" is pure cost for you: code to write, bugs to have, context for the model to load, latency to pay. The design principle underneath everything below is: **build the smallest thing that is still reliable for one person doing narrow, repeated coding work, and cut anything whose only job is serving more than one person or more than one trust level.**

This produces a system that's smaller than Claude Code in surface area but not necessarily less capable where it matters, because the capability that matters here is "did this specific edit work," not "can this scale to an org."

## What gets cut, and why it's safe to cut

| Claude Code feature | Cut for this design | Why it's safe |
|---|---|---|
| Four-tier config hierarchy (managed/user/project/local) | Yes, one config file | There's no fleet to manage, no "which layer wins" debugging to do |
| ~30 hook events | Yes, down to 2: pre-tool-use, post-tool-use | Every other hook point (session lifecycle, worktree, MCP display events) exists to instrument enterprise workflows this design doesn't have |
| OpenTelemetry export, CSV usage export, per-user analytics API | Yes | These are audit features for someone else's spend. You're the only spender and you're already looking at the terminal |
| Multi-agent "teams," nested subagent hierarchies past one level | Yes | Anthropic's own numbers put this at ~15x token cost, justified only for genuinely parallel, high-value work. Narrow single-developer edits are sequential by nature |
| Enterprise permission modes (`auto` requiring Team plan, managed policy overrides) | Yes | One-person tools don't have a policy administrator |
| Formal eval-harness-driven prompt A/B testing (pass@k/pass^k machinery) | Yes, replaced by manual transcript review | That machinery pays for itself at Anthropic's iteration volume. For one harness, reading failures and adjusting is the right-sized version, and it's what Anthropic itself recommends as the starting point before scaling up |
| Bespoke sandbox runtime as a day-one build | Deferred, not cut | Permission gate plus deny-list catches the documented incident pattern (PocketOS, Replit, the firmware `rm -rf ~/` case). Seatbelt sandboxing is the next investment once the harness is trusted enough to run with fewer prompts, not a prerequisite |

What survives, because it's cheap and it's exactly where the 2025-2026 incident record shows things go wrong: a permission gate in front of anything destructive, defaulting to ask; a single flat instruction file; basic compaction with a manual override; bounded retry with no silent tool-call drops; per-session token/cost visibility; one or two context-hygiene subagents (an Explore-equivalent).

## Architecture

Fork Crush (`charmbracelet/crush`), don't build the core loop from zero. It's Go, Bubble Tea, has the best local-model support of anything surveyed, and is actively maintained. Rewriting a tool-use loop, a virtualized scrollback list, syntax-highlighted diffing, and goroutine-safe streaming from scratch would mean re-solving problems Crush has already solved and battle-tested, for no capability gain. See [research/existing-alternatives-landscape.md](research/existing-alternatives-landscape.md) for the fork-viability analysis; validate this by prototyping the routing layer (below) against Crush's actual code for a day before committing.

The subsystems that don't exist anywhere yet, and that make this a genuinely different tool rather than a Crush clone, layer on top:

1. Deterministic gate system (non-AI verification, selectively scoped, cheap by construction)
2. Context provenance / explainability log, doubling as the substrate for line-level diff Q&A and structural session continuity
3. Hybrid local/cloud model router
4. Tool and MCP surface pruning
5. Remote/mobile access, a hard requirement, not deferred, plus a self-comparison benchmark harness

Each is described below as a design, not just a feature list. Mobile is called out as hard-requirement throughout because it changes a few decisions elsewhere: notifications need to be worth interrupting a phone for (batched, not per-event), and the line-level Q&A design has to work identically from a touch UI and a terminal, not as a TUI-only feature with a mobile afterthought.

## 1. Deterministic gates

Non-AI verification, wired into the tool-use loop at the two hook points that matter (pre-tool-use, post-tool-use), replacing the ~30-event surface Claude Code exposes with exactly the two junctures the incident record shows matter. This is the single highest-leverage piece of the whole design: it costs zero LLM tokens, it's exactly repeatable, and it directly targets the most common documented failure mode across the harness research (agent claiming success without a real check having run since the last edit). Build it first.

**Changed-file detection, VCS-agnostic.** Before selecting which tests to run, the gate needs to know what actually changed since the last passing gate run, not since some arbitrary session boundary. Two backends, same interface:

- Git: `git diff --name-only <last-gate-sha>` (or against the working tree if uncommitted).
- Jujutsu: `jj diff -s --name-only --from <last-gate-op>` for the file list, or `jj operation diff --from <op> --to @` when the comparison needs to span multiple operations rather than a single revision (confirmed against jj's own CLI reference, [docs.jj-vcs.dev/latest/cli-reference](http://docs.jj-vcs.dev/latest/cli-reference/), 2026-08-12: `jj diff` takes `-s`/`--name-only` for a bare changed-file list, and `jj operation log`/`jj operation diff` are the op-log-native equivalents of `git log`/`git diff` for this purpose).

Either way, the gate stores its own last-known-good marker (a commit SHA or op ID), not a session ID, so "what changed" survives across session boundaries by construction, without needing the agent to remember anything.

**Selective test execution via coverage mapping, not naive file-to-test guessing.** The pattern to copy is what `pytest-testmon` already does for Python: run once with coverage instrumentation on, build a persistent map of which tests exercise which lines, then on every subsequent run compute the diff against that map and execute only the tests whose covered lines actually changed (confirmed via [testmon.org](https://testmon.org/), 2026-08-12). This generalizes past Python: Go's built-in coverage profiles (`go test -coverprofile`) and most language coverage tools produce the same line-to-test mapping shape, so the gate's selection logic can be one coverage-map abstraction with a thin per-language adapter (testmon for Python, a coverage-profile parser for Go, Istanbul/nyc-derived maps for JS), rather than reimplementing test selection per ecosystem. Nx's `affected` command is the other reference point for the same idea at a coarser grain (project/package level via a dependency graph rather than line-level coverage, confirmed via [nx.dev/ci/features/affected](https://nx.dev/ci/features/affected), 2026-08-12): worth the coarser-grained fallback for monorepos where line-level coverage mapping is too expensive to maintain, running only the affected package's suite rather than the affected tests specifically.

**Staleness-triggered full runs.** Coverage maps drift: a new file has no coverage history yet, and some failure classes (flaky tests, integration tests touching shared state, tests whose behavior depends on something outside the coverage-tracked call graph) won't show up in a selective run regardless of how good the mapping is. Trigger a full run on a cadence, not on every gate: after N selective-passing gates in a row, after a time threshold since the last full run, or immediately if the coverage map itself flags an untracked file. This keeps the common case (edit, gate, edit, gate) fast while still catching the failure classes selective running structurally can't.

**Output trimming, aggressive.** On full pass: nothing goes back to the model, not even a summary, just a boolean/timestamp written to the gate log. Spending tokens to tell the model "tests passed" when the model didn't ask is waste; the absence of a failure signal in the next turn's context is the signal. On failure: only the failing test names and the portion of the stack trace that references a file touched in this change, not the full test-runner output (which for most frameworks is mostly noise, setup/teardown logs, passing-test names). This is a parsing problem, not a summarization-by-LLM problem: extract structurally from the test runner's own output format (JUnit XML, `go test -json`, pytest's own machine-readable output), don't ask a model to compress test output, that's slower and costs tokens for a mechanical extraction task.

**Concurrency control.** Gates need a single-flight scheduler keyed by resource, not by request: two edits landing close together against the same project should coalesce into one gate run covering both, not run twice against overlapping file state. Gate types that don't share a resource (lint and typecheck against static files, versus a test run that might spin up a dev server or touch a shared test database) can run in parallel; gate types that do share a resource serialize. A short debounce window (batch edits arriving within, say, 2-3 seconds into one gate run) avoids re-running gates on every individual tool call when the agent is making several rapid small edits in sequence.

**Configuration:** one file per project, the test/lint/typecheck commands, discovered from existing project config (`package.json` scripts, a `Makefile` target, `pyproject.toml`) where possible, with a fallback prompt to set it explicitly on first run. No per-directory override hierarchy, no managed policy, one answer per project.

## 2. Context provenance, explainability, and line-level diff Q&A

The design goal: answer "what influenced this decision" from a log, not from asking the model to explain itself after the fact. Model self-report of its own reasoning is unreliable, models rationalize plausible-sounding explanations that don't necessarily match what actually drove the output, so the mechanism has to be structural, not conversational. This same structural log turns out to be the right substrate for two more of your requirements, line-level diff Q&A and session continuity, so it's worth building once and reusing, rather than as three separate features.

**The context manifest.** Every context item that enters a prompt gets tagged at assembly time, before the model call, with `{source, id, reason}`: `reason` is one of `explicit_read`, `grep_hit`, `memory_recall`, `ledger_entry` (see session continuity below), `instruction_file`, `tool_result`, `prior_turn`. Log the full manifest alongside the request, not derived after the fact by re-parsing the transcript. This is nearly free: you're already assembling this context, the only new work is writing down what you assembled instead of discarding it once the prompt is built.

**Diff-line anchoring.** Every line in a generated diff gets a stable anchor: `{file, line_range, content_hash, turn_id}`, computed when the diff is produced, not derived later from the rendered view (rendered line numbers shift as the diff view scrolls or re-renders; content hashes don't). The anchor links a line back to the exact turn and context manifest that produced it, which is what makes "why did the agent write this" answerable by lookup rather than by asking the model to remember.

**Ask-a-line, same mechanism on TUI and mobile.** In the Bubble Tea diff view, select a line and open a scoped question (a `?` or dedicated keybinding opening a small input, not a full new session). On the PWA, tap a line in the same diff view and get the same input. Either way, the question is answered using a deliberately narrow context: the anchored line, its surrounding hunk, the file's structural neighborhood (a tree-sitter symbol outline of the enclosing function/class, not the whole file), and the context-manifest entries for the turn that produced it, not the full session history. This keeps a line-level question cheap, closer to the cost of a single focused tool call than a full conversational turn, and it composes with the router: a line-level question is almost always a "stays local" task by the router's own task-shape heuristic (below), since it's narrowly scoped by construction. Store the Q&A thread itself as another entry keyed to the same anchor, so a line accumulates a visible thread of questions and answers the way a PR review comment does, browsable later without re-asking.

**Structural continuity across sessions, instead of one long compacted session.** You start new sessions frequently by preference, and the ask is to make that cheap rather than making you stop doing it. The insight from the harness research worth leaning on: every tool's compaction (Claude Code's five-stage pipeline, Goose's threshold-plus-strategy, OpenCode's still-buggy version) exists to manage a single ever-growing *conversation*, and conversation is the expensive, lossy thing to carry forward. It doesn't have to be what carries forward. Split what a session produces into two categories:

- **Session state**: the actual conversation, expensive, thrown away when the session ends. No cross-session compaction to build or maintain.
- **Project state**: structural facts that are already cheap because they're derived from ground truth, not from the model's account of itself, ready to reuse without re-summarization:
  - the gate log (what changed, what passed, what's stale) from section 1
  - the context manifest and diff-line anchors from above
  - a small append-only **session ledger**, one line per session, written by structural extraction wherever possible (files touched, gates run, router escalations) and by a single brief model-written handoff note only where structure genuinely can't capture it (an open question, a deliberate decision not to fix something yet)

A new session reads the ledger's last few entries plus the current gate/git state, not a summarized transcript. That's a few hundred tokens of structured fact, not a compacted-and-therefore-lossy multi-thousand-token conversation history, and it composes with the manifest: if the new session needs to know *why* a past decision was made, it follows the anchor back to the original turn's manifest on demand rather than carrying that explanation forward speculatively in every new session whether needed or not. This is the actual answer to "linked sessions without the cost of excess context": don't link the conversations, link the structural record, and let file content and git state (which are already sitting on disk, free to re-read) stand in for what a compacted summary would otherwise be reconstructing from memory.

## 3. Hybrid model router

Every tool surveyed supports both local and cloud providers, but none of them route between them per-request based on task shape. That's the actual gap, and it's a small, well-scoped piece of code sitting in front of the existing tool-use loop.

**Routing signal, in priority order:**

1. **Task shape**, inferred from the pending action, not the prompt text. A single-file edit under N lines with no cross-file dependency, or a diff-line question (section 2), stays local. A multi-file refactor, a task that already failed once locally, or anything requiring more than the local model's practical working context (8K-32K tokens on a 64GB Mac, per [research/local-inference-apple-silicon.md](research/local-inference-apple-silicon.md)) escalates to the cloud model immediately, not after burning a failed local attempt.
2. **Prior failure on this exact task.** One local failure (malformed tool call, repetition loop, wrong-file edit, or a gate failure the local model can't resolve after one retry) escalates for the remainder of that task. Don't retry local past one failure; the local-model failure modes documented in research (qwen2.5-coder emitting tool calls as markdown text, qwen3:14b looping 10 times burning 30K tokens) are not the kind that resolve on retry.
3. **Explicit override**, a flag or keybinding to force local or force cloud for a given turn, because you will sometimes know better than the heuristic.

**What this buys you:** local-only cost and offline capability for the bulk of routine edits, with the reliability gap (Epoch AI's 3-4 month agentic capability lag, concentrated in sustained multi-step execution) covered by falling back before it costs you a broken session, not after.

**What this explicitly does not do:** semantic routing based on prompt content, model-graded complexity scoring, or anything that itself costs an LLM call to decide. The router should be cheap, deterministic-ish code, not another agent.

## 4. Tool and MCP surface pruning

The gap this closes isn't just token cost, it's decision quality: tool-selection accuracy degrades as the number of available tools grows, which is exactly why Anthropic's own tool-design guidance (cited in [research/agentic-harness-architecture.md](research/agentic-harness-architecture.md)) is about consolidating and clarifying tool descriptions, not adding more of them. A single-user tool can go further than a general-purpose one here, because the tool surface only has to cover what your actual projects use, not every workflow a general audience might need.

- **Per-project tool manifests, not one global tool list.** A project that doesn't use Docker doesn't get Docker tools loaded into context for that session, full stop, not deferred-but-still-named. Derive the manifest from what's actually detectable in the project (lockfiles, config files, an explicit small `.agent/tools.yaml` allowlist) rather than exposing every tool the harness knows how to run everywhere.
- **One generic execution primitive plus an allowlist, instead of many named wrapper tools where a wrapper adds no real capability.** If a "tool" is just `run("docker build ...")`, it doesn't need its own schema and name distinct from a general shell-run primitive; give the model fewer, clearer choices rather than a long menu.
- **MCP servers connected on demand, not all up front.** Same principle as the router: don't pay the context and connection cost of an MCP server for the whole session because one task in it might use one tool from one server. Connect when a task's tool manifest calls for it, disconncet after.
- **A periodic audit, not a one-time design.** Since gates and the ledger (sections 1-2) already log which tools actually get invoked per project, use that log to prune: a tool unused across N sessions for a given project is a candidate to drop from that project's manifest, reviewed by you, not auto-pruned silently.

## 5. Remote, mobile, and self-benchmarking

Mobile notifications and review are a hard requirement, not a lower-priority nice-to-have, so this moves up in the build order relative to the earlier draft. Two things follow from "hard requirement" that wouldn't follow from "eventually":

- **Notifications are batched and judged worth an interruption, not fired per event.** Given gates run frequently and mostly pass silently (section 1), a push for every gate result would be noise; push on gate failure that needs a decision, on session end with a ledger summary, or on an explicit "needs your input" state, using the same ntfy.sh pattern from [research/remote-mobile-access.md](research/remote-mobile-access.md).
- **The line-level diff Q&A view (section 2) has to be a first-class mobile surface, not a TUI feature ported later.** Design the anchor/thread data model in section 2 to be transport-agnostic from day one (a small JSON object served over the local HTTP API, rendered by both the Bubble Tea view and the PWA), rather than building it against Bubble Tea's data structures first and translating afterward.

Otherwise, the design from [research/remote-mobile-access.md](research/remote-mobile-access.md) stands: Tailscale (mesh, Funnel only if reachability beyond your own devices is required), a QR-pairing flow minting a device-scoped bearer token, a PWA hitting the local HTTP API, ntfy.sh for push. No native app, no Wish/SSH server, until the PWA proves insufficient.

The benchmark harness is unchanged in design from the synthesis: reuse Terminal-Bench or Harbor for task/scoring, Claude Code's own OTEL export or Inspect AI's `sandbox_agent_bridge()` for cost/token accounting, a thin wrapper tying them together that also reads this tool's own gate log and ledger to add turn count and gate-failure count to the comparison, metrics none of the reused tools capture.

## What else, in the same spirit

A few more levers worth building because they're cheap, structural, and directly reduce either token spend or wrong turns, the same criteria everything above was chosen against:

- **Read-once file cache keyed by content hash.** If a file hasn't changed since it was last read into context (checkable against the gate system's own changed-file detection), reuse the cached content instead of re-issuing a read tool call. Zero LLM cost, purely a dedupe layer.
- **Diff/hunk context instead of whole-file context for follow-up edits.** When continuing work on a file already touched this session, feed the diff since last touched plus a tree-sitter symbol outline rather than the full file text again, the same instinct behind Aider's repo-map approach (documented in [research/agentic-harness-architecture.md](research/agentic-harness-architecture.md)) applied at the single-file level.
- **Deterministic formatting and lint-autofix before the model ever sees a diff.** Run the project's formatter and any auto-fixable lint rules as a pre-pass, not a gate the model has to react to; trivial style issues shouldn't consume a turn or a token.
- **A structural symbol index (tree-sitter or ctags) for "where is X defined" instead of multiple grep-and-read round trips.** This is a deterministic lookup replacing several probabilistic tool calls, cheaper and more reliable than letting the model explore its way there.
- **Speculative gate pre-warming.** Start compiling/warming the test binary for the affected package while the model is still generating the edit, so gate latency overlaps generation latency instead of stacking after it. Purely a scheduling optimization on top of section 1's gate design, no new architecture.
- **Prompt caching on by default wherever the harness makes repeated calls with a stable prefix** (system prompt, tool manifest, ledger entries), a 90% discount on cache hits per Anthropic's published pricing, essentially free money once cache breakpoints are placed correctly.

## Build order

1. Deterministic gates (section 1): highest reliability leverage, zero token cost, smallest surface area, and the changed-file/coverage machinery here is reused by nearly everything after it.
2. Context manifest, diff-line anchoring, and the session ledger (section 2): near-free once gates exist, since both hook into the same tool-call lifecycle and the same changed-file detection.
3. Mobile basic version (section 5), pulled forward because it's a hard requirement: Tailscale, QR pairing, PWA, ntfy, and the diff Q&A view built transport-agnostic from the start rather than ported later.
4. Hybrid router (section 3): the piece that makes "local-first, cloud-fallback" actually true rather than aspirational.
5. Tool/MCP pruning (section 4): easiest to do well once the gate/ledger logs exist to show what's actually used.
6. The "what else" efficiency layer, applied incrementally as each piece proves itself, not built as a batch.
7. Benchmark harness last: measuring a system that isn't reliable and cheap yet produces numbers that won't hold once 1-6 change them.

## Open questions

- Whether the router's "task shape" heuristic needs to be learned from your own usage over time, or whether a fixed rule set (file count, line count, prior-failure flag) is sufficient. Start fixed; only add learning if the fixed version misroutes often enough to be annoying.
- Whether the deterministic gate's project-command discovery, and the coverage-map abstraction, need to handle monorepos with per-package test commands on day one, or whether that's out of scope for a first version scoped to your actual current projects.
- Whether a per-language coverage-map adapter (testmon for Python, `go test -coverprofile` parsing for Go, an Istanbul/nyc-derived map for JS) needs to exist for every language you actually use before the selective-gate design pays off, or whether a coarser Nx-style affected-package fallback is good enough at first and line-level selection is a later refinement.
- How much of the session ledger's non-structural portion (the brief model-written handoff note for things structure can't capture) needs to exist at all in practice, versus how often the structural facts alone (gate log, manifest, git state) turn out to be sufficient without it. Worth measuring after a few weeks of real use rather than deciding up front.
