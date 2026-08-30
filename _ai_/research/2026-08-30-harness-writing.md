# What other people writing agent harnesses have found (2026-08-30)

A delegated pass over [Vercel's Build an AI Agent Harness
course](https://vercel.com/academy/build-ai-agent-harness) (15 of its 38
lessons read) plus a search for comparable primary writing. Two of its
findings were checked against this repository and are wrong about it, which
is noted below rather than dropped, because the same mistake is available to
anyone reading only the roadmap.

## What the course argues

Its throughline is that a naive tool loop degrades in measurable ways and
each module fixes one, shown with an instrumented before-and-after rather
than asserted. The opening measurement is input tokens climbing from ~1,200
to ~9,200 over four steps while output stays flat, and it explicitly rejects
"use a bigger model" and "cap the step count" as answers because neither
touches the growth curve. That is the same argument this project's preamble
accounting makes from the other end.

Its fix is layered: bound tool output at the source (a 500-line read cap with
pagination, a 50-match grep cap, a 5,000-character shell cap that keeps the
**tail** because errors live at the end), then prune stale tool results before
each call, then mark what survives for provider caching. The load-bearing
point is that every truncation must tell the model it happened, since a
silently cut result is worse than no result: the model cannot know to ask for
more.

## What is worth taking

**Overflow belongs in a file, not in the ellipsis.** OpenDev
([arXiv 2603.05344](https://arxiv.org/html/2603.05344v1)) writes a tool
result too large for the window to a scratch file and hands the model a path
plus a preview, so the part that was cut stays reachable. This project
truncates and says how much it dropped (`... [N lines omitted] ...` in
`internal/tools/shell.go`, and the same shape in compaction), which tells the
model that something is missing and gives it no way to get it. A run that
needs line 400 of a 900-line test failure currently has to re-run the command
differently. Small to build: the state directory already exists, `read`
already takes a line range, and the truncation site is one function.

**Compaction can be tested for recall rather than read for plausibility.**
[Context Compaction Theory](https://arxiv.org/html/2608.01326v1) proves the
minimum viable budget for a compaction algorithm equals the one-way
communication complexity of the query it must later answer, and separately
measured a production compaction endpoint answering membership queries close
to a random guess, worse than a Bloom filter of the same size. The useful
part here is the method: test whether a compacted transcript can still answer
point questions about what happened, rather than whether the summary reads
well. `internal/thread/compaction.go` is deterministic (dedupe, drop, keep
first and last lines) so it has no summarization to lose facts in, but the
same test would catch a future change that added one.

**Quality has axes other than the outcome.** Outcome, process, style, and
efficiency, with expensive judging reserved for where it changes a decision
([FutureAGI](https://futureagi.com/blog/agent-eval-harness/), secondhand and
unverified). The replay harness records outcome and efficiency already. Style
needs no judge here because the `lint` gate is one. Process is the genuinely
missing axis and the instrumentation is already there: whether a run searched
before reading, how many of its reads were repeats, how many tool calls it
spent per accepted change, all of which `wavez -stats` computes per run and
nothing scores across runs.

**Delegation, if it ever happens, ties the tools to the role rather than the
model.** The course's explorer is a small model with read and grep and a
5-step budget; its executor is a larger one with a literal command allowlist
and 15 steps, told not to ask clarifying questions because that is the
parent's job. Against that, OpenDev found a class hierarchy of specialised
agents produced "a diamond problem when subagents needed mixed capabilities"
and settled on one parameterised agent. This project's tiers are already
parameters rather than types, which is the OpenDev position.

## Where it disagrees with this project, and who has the measurement

The course's tool-description template is five or six sections per tool
(summary, when to use, when not to use, do not use for, usage, examples) and
it argues negatives must be stated twice, once softly and once as a hard
boundary, because a model defaults to the most general tool when a
description is ambiguous.

This project measured the opposite trade and went the other way: the tool
surface was 84% of the preamble and 69% of that was prose, so failure-mode
prose moved out of the schemas and into the errors, from 3,029 tokens to
2,736. The two are not really in conflict about what a model needs, only
about who pays: the course's tokens are spent on a hosted model with a large
window, and a fast turn here has 7,168 tokens to work with, of which the
preamble is already 37%. Where the observation does apply is the `shell`
gravity it names, which this project has hit twice and fixed twice in the
errors rather than the descriptions, once for stream editors and once for
re-running the gates.

## Two claims about this repository that are wrong

Both come from reading the roadmap without the code, which is worth recording
because the roadmap invites it.

- "I found no evidence of a compaction or memory layer." There is one:
  `internal/thread/compaction.go`, with `DedupeToolReads` replacing a
  byte-identical earlier tool result and a first-and-last-lines rule over
  what survives. `internal/reduce` dispatches tool output to a reducer by
  shape before any of that runs
- Explicit `cache_control` markers, from
  [Anthropic's cookbook](https://platform.claude.com/cookbook/tool-use-context-engineering-context-engineering-tools),
  do not apply. Every endpoint here is OpenAI-compatible and caches by prefix
  automatically, which is where the measured 77% of input tokens served from
  cache already comes from

## Sources

- [Build an AI Agent Harness](https://vercel.com/academy/build-ai-agent-harness),
  15 of 38 lessons read
- [Building AI Coding Agents for the Terminal, arXiv 2603.05344](https://arxiv.org/html/2603.05344v1)
- [Context Compaction Theory, arXiv 2608.01326](https://arxiv.org/html/2608.01326v1)
- [Anthropic cookbook: context engineering](https://platform.claude.com/cookbook/tool-use-context-engineering-context-engineering-tools)
- [MCP vs CLI token costs](https://www.mindstudio.ai/blog/mcp-vs-cli-ai-agents-token-costs-when-to-use),
  which puts a tool definition at 150-600 tokens resent per call and agrees
  with the Decisions entry above it
- [Agent eval harness](https://futureagi.com/blog/agent-eval-harness/),
  reached through search results rather than fetched
