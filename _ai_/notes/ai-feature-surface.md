# Which harness features wavez should have at all

Open decisions, not yet folded into `DESIGN.md`. The question behind each is the same:
a feature every other harness ships is not thereby justified here, because wavez's
thesis is that the predictable parts run deterministically and the model is kept for
judgment. A feature earns its place if the harness can act on it. It does not earn its
place by telling the model something and hoping.

Two mechanical constraints decide more of this than taste does:

- **The prefix must stay byte-identical turn over turn.** Compaction appends and never
  rewrites for this reason, measured at 5-7x the cost of an append when the middle of a
  cached prefix moves. Anything that changes what is in context *partway through a task*
  pays that
- **Model output is never a policy input.** Approval comes from the deterministic checker
  or the user. Anything where the model decides what it is allowed to know, or do, next
  runs into this

## Skills

Claude Code's shape: a directory per skill, a description always in context, a body
loaded when the model judges it relevant, optionally with scripts and reference files
beside it.

Decomposed against what wavez already has, a skill is four different things:

| Part of a skill | wavez's existing answer |
|---|---|
| A rule the agent must obey | `ast-grep` convention rules, enforced on every change set |
| A procedure with commands | Routines (M2), a pkl DAG with an action registry |
| A phased way of working | Cycles (M2), whose phases exit on a Condition the harness evaluates |
| Voice, style, standing context | The `context` list in `.wavez.pkl`, files or headed sections |

What is left over after that decomposition is the *trigger*: loading something into
context because the model judged it relevant to this task. That is the part that costs
something real here, not philosophically but mechanically. Conditional context is a
prefix that changes partway through a task, which is exactly the case compaction is
built to avoid, and it makes the router's context estimate a moving number. `DESIGN.md`
already records the decision against Skill-style prompt bundles in favour of Cycles; the
cache argument is the stronger half of it and is not written down yet.

The honest cost of saying no: authoring is harder. A skill is one markdown file, where
the decomposition above is a YAML rule plus a pkl routine plus a context entry. That is
a real ergonomic loss and worth stating rather than pretending the four-way split is
free.

**Leaning: no Skills.** Write the cache argument into the existing decision, and treat
"this was hard to author" as the signal to improve Routine and rule authoring rather
than to add a fifth concept.

## Hooks

Two shipped, pre-tool-use and post-tool-use, as external commands with JSON on stdin and
the exit status as the verdict. Other harnesses carry six to eight events.

Judge a candidate event by whether a deterministic program can do something useful at
it that wavez does not already do:

| Candidate | Verdict |
|---|---|
| Session start | Covered. The `context` list and the ledger are the seed, and both are already deterministic |
| User prompt submit | Mostly covered. `@file` and `@symbol` expansion already runs there. A hook could enforce a project policy on prompts, which is a real but thin case |
| Thread finished | **Not covered.** Notifications are M2 and this is where they hang. Also where a "post a summary" or "kick CI" integration belongs |
| Pre-compaction | Weak. Compaction is deterministic and append-only, so there is nothing for a hook to decide |
| Permission asked | Redundant. Pre-tool-use already runs behind the guard and the gate, and a second veto point at the same moment adds no power |

**Leaning: hold at two, add thread-finished when M2 notifications land.** It is the only
one with a job the harness cannot already do.

## Scratchpad

wavez already has the directory: `.wavez/sessions/session-*`, created per App, inside
the Seatbelt write scope, with `GOCACHE`, `GOMODCACHE`, and `GOTMPDIR` redirected into
it, and removed on `Close`. What it does not have is any way for the model to know it
exists.

The consequence is visible today. A model that wants somewhere to put a scratch script
writes it into the repository, where it lands in the change set, shows up in the diff
pane, and trips the scope tracker as a file the run created rather than read. The
machinery to keep that out of the way is already built and simply not connected to
anything the model can aim at.

**Leaning: expose it**, as a path named in the system prefix, filtered out of the change
set and the diff the way `.wavez/` already is, and left to `Close` to delete. The risk
worth naming: work hidden in a scratchpad is work the gates do not see, so nothing that
runs from there should count as a change, and `-strict-scope` should treat it as
in-bounds rather than as an unread file.

## Other features, with less doubt

| Feature | Position |
|---|---|
| A live todo list the model maintains | No. The durable half is the ledger and a Cycle's phases, both of which the harness owns. A model-authored task list is a claim about progress, which is the thing Cycles exist to stop trusting |
| Persistent memory across sessions | No separate system. Project state is the store, the gate log, and the ledger; standing instructions are the `context` list. A "remember this" verb would be a write to one of those, not a new one |
| Sub-agents beyond one level | Already decided: one level of delegation, sub-threads in M2, no hierarchy |
| MCP | Already decided: M3, connected per thread on demand from an allowlist, never all up front |
| Web search | Already decided: M3, one search tool and one fetch tool, results trimmed before the model sees them |
| Output styles and personas | No. Nothing in the thesis improves when the model writes differently |
| Slash commands | The palette already is this, and it dispatches to verbs the harness owns rather than to prompt text |

## What to do next

Fold the decided ones into `DESIGN.md` as y-statements, since the value of that file is
that a decision is recorded once with its reason and not re-argued. The scratchpad is
the only one here that is a build rather than a position.
