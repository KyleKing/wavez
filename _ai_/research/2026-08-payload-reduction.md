# Payload reduction, 2026-08-21

What a harness can cut from a tool result before the model sees it, and what
that is worth. Written from a web research pass dated 2026-08-21 plus
measurements taken on this repo's own thread logs. Every number is labeled
**measured** (a method and a sample size are published), **claimed** (asserted
without one), or **local** (taken here, method stated inline).

## What this repo actually spends

The audit's cost table assumed the payloads were the cost. On the fixed task
set they are not, and the shape of the spend decides which technique is worth
building.

- **local** A run's whole tool-result volume is small. `e3`, eighteen turns,
  returned 7,628 bytes across nineteen calls: `str_replace` 891, `read` 1,482,
  `shell` 1,562, `context` 2,313, `search` 1,380
- **local** The fixed preamble is the largest single line item. Turn one of a
  run costs 2,826 input tokens before any content, and it is re-sent every
  turn. Across ten recent runs it is 25% to 82% of all input tokens, median
  about 55%. Of it, the eight tool specs are 6,293 bytes (`list` 679, `read`
  1,013, `str_replace` 1,623, `write` 509, `shell` 524, `search` 813,
  `context` 701, `question` 431), so roughly half the floor is schema
- **local** The model pays turns to shorten output itself. 161 of 258 shell
  calls across the thread logs (62%) pipe through `head`, `tail`, `grep`,
  `wc`, `sed -n`, or `awk`, or redirect stderr, to control a truncation the
  harness was doing badly
- **local** Head-and-tail truncation loses the answer on exactly the output
  that matters. A 126-line `go test -v` run with one failure: the fixed
  twenty-line windows keep 1,060 bytes and drop the assertion message, the
  failing subtest's name, and the parent `--- FAIL`, leaving three bare
  `FAIL` lines

## Techniques surveyed

**Content-aware structural reduction.**
[toolshrink](https://github.com/unclecode/toolshrink) (MIT, by the author of
Crawl4AI) intercepts tool output and dispatches to one of thirteen reducers by
detected type: tests, build, diff, logs, tree, stacktrace, json, and a generic
size fallback. Four rules govern every reducer: never emit a partial line,
preserve string integrity, state what was omitted rather than dropping it
silently, and stay idempotent on a second pass. A file spill store keeps the
original for 24 hours behind a locator so the full output stays reachable.
**claimed** One example, a vitest run: 31,958 chars to 255, against 1,904 for
head-and-tail at the same 2,000-char budget. No eval suite and no downstream
quality number. It ships as a plugin for
[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness), which is
real and in developer preview, built on Cordis with everything including the
agent loop as a plugin; its own README lists no context-reduction plugins, and
the star counts in secondary coverage are unverified.

**Hybrid grep-and-tail routing for CI logs.** The one rigorously benchmarked
result found. [LogDx-CI (arXiv:2605.28876)](https://arxiv.org/abs/2605.28876)
compares eleven reduction methods over 35 real GitHub Actions failures, scored
by three model families plus a tool-using agent. **measured** The best hybrid
routers score 0.670 and 0.666 at about 19.8k tokens per case, against grep
alone at 0.639 quality and 88,355 tokens, so roughly 4.5x fewer tokens at
matched quality. The finding that bears on this design is the second one: in
the agent-loop regime, where the agent can issue follow-up calls, quality
spread across all eleven methods collapses sevenfold (0.42 to 0.059), and it
does so by spending two to four times the tool calls. A weak first payload is
not lost quality here, it is turns.

**Context compaction across shipped scaffolds.**
[Inside the Scaffold (arXiv:2604.03515)](https://arxiv.org/abs/2604.03515)
taxonomizes thirteen open-source coding agents and finds seven distinct
strategies: rule-based truncation (SWE-agent keeps first and last N
observations, with a parameter that preserves prompt-cache compatibility),
structural isolation (Prometheus scopes messages per node), token-budgeted
selective inclusion (Moatless), progressive file reduction (Agentless
compresses function bodies to signatures with libcst past 128k), LLM
summarization (Aider, Codex CLI, OpenCode), a post-summary verification probe
(Gemini CLI), and model-initiated compaction (Cline). It is a taxonomy with no
aggregate savings number. One scaffold implements none and crashes on a full
window.

**Outline-mode reads.** Several tools return a file's symbol skeleton and the
body only on request
([ast-outline](https://github.com/ast-outline/ast-outline), CodeRLM,
jcodemunch-mcp). **claimed** 95%-plus cost cuts, self-reported, no method.
This repo already has the substrate (`internal/codeintel`, `@symbol` expanded
to a signature), so the question here is whether `read` should default to it.

**Deterministic pre-compaction cleanup.** Drop duplicate reads keeping the
newest, prune errors already resolved, normalize stack traces, before any
model-based summarization runs. **claimed** 15-30% with no information loss,
from a secondary source with no benchmark behind it. `RepeatReads` and
`RepeatReadBytes` already count the first of these here.

## What follows for this repo

Ranked by what the local numbers support rather than by the survey's own
order.

1. **Reduce shell output by shape, before any size cap.** Built. See the
   `internal/reduce` bullet in DESIGN's Next
2. **Shrink the fixed preamble**, since it is half the input of a median run
   and half of it is tool schema. The lever is per-tool: `str_replace` alone
   is a quarter of the schema bytes, and Modifiers would let the description
   stop explaining exact-match anchors
3. **Outline-mode `read`**, once a task set exists whose retrieval is the hard
   part. `h1` is the first
4. **Dedup of near-identical results across turns.** The measured case here is
   three byte-identical failing edits in one run, which the loop already
   catches as stagnation, so the payload half needs a task that produces
   near-identical rather than identical results before it is worth building
5. **A spill store** for anything a reducer dropped. The Compaction section
   already specifies the shape (the omission marker names a file id under the
   session directory and the existing `read` tool fetches it, so no new tool
   enters the prefix) and nothing builds it. It stays behind the four above
   while every reducer states its omissions and the model can re-run the
   command

The LogDx-CI agent-loop result is the caution over all of this. Reduction that
costs the model a follow-up call has moved the spend rather than removed it,
so every reducer here is judged on turns as well as bytes.
