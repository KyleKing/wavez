# Spike: what is actually in a long context

Feeds the Cycles section of [DESIGN.md](../../../DESIGN.md). The goal stated
for that work is to kill the long context by architectural design, on the
claim that it is not needed. This measures whether the claim is true, using
real transcripts rather than an argument.

## Method

`measure.py` reads this project's own thread event logs and splits every
event by whether it can be produced again from the code and tools:

- **Re-derivable**: model prose, tool output, bookkeeping. Running the same
  tool against the same tree returns the same thing, and the prose largely
  restates what the tools did
- **Not re-derivable**: the user's goal and mid-run feedback, and gate
  verdicts. Nothing in the repo tells you what was asked or what a gate said
  at a moment in time

Tokens are chars/4, the same rough estimator `internal/agent` already uses
for routing. The ratios are the point, not the absolute counts.

## Numbers

Eleven threads from the 2026-08-16 session, 318 KB of event log:

| Category | ~tokens | Share | Re-derivable |
|---|---|---|---|
| Model prose | 59,329 | 74.6% | yes |
| Tool output | 16,857 | 21.2% | yes |
| Bookkeeping | 1,395 | 1.8% | yes |
| Goal and feedback | 1,152 | 1.4% | no |
| Gate verdicts | 767 | 1.0% | no |
| **Total** | **79,502** | | |
| **Not re-derivable** | **1,919** | **2.4%** | |

The session's single largest investigation (why hosted runs reported success
having changed nothing) spanned several of those threads and distills to
`ledger-example.md`: five hypotheses, five experiments, five verdicts, and
the cause. **360 tokens.**

## What it does and does not establish

It bounds what is at stake. 97.6% of what accumulated is reproducible on
demand, and re-derivation cannot go stale the way a carried summary can,
which the 2026 agent-memory literature names as its own unsolved problem.

It does **not** establish that model prose is re-derivable verbatim, and the
classification is the arguable part of this spike. The prose contains
reasoning that appears nowhere in the code. The claim is narrower: the
durable content of that reasoning is the falsified-hypothesis set, that set
is small and typed, and the 74.6% is an upper bound on what is lost by
keeping only it. Whether a phase carrying 360 tokens instead of 79,502
produces equally good work is the next spike, and it is not measured here.

## Where the rest lives

Attempts do not need to be written anywhere: jj snapshots the working copy
on every command, so each one is already addressable by operation id.
Measured separately during the same session: squashing a commit away leaves
`jj log` without it while `jj --at-op <earlier> file show -r <change>` still
returns its content, and the op log grew from 9 entries to 10 rather than
shrinking. History rewriting does not touch the operation log.

The op log is local, though, so this is working memory, not institutional
memory. A GitHub squash-merge collapses a branch's commits, and a fresh
clone has neither those commits nor any op log. Anything meant to survive a
clone has to be a tracked file. That is the sharpest argument for the
generalize phase: it is the only phase whose output (a rule, a helper, a
test, a note) is durable at all.
