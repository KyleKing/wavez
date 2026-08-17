# _ai_

Research, notes, and demos behind [DESIGN.md](../DESIGN.md). Nothing here is source. Standalone projects that started here now live beside this repo, with a condensed note kept where the design still leans on them.

## Layout

| Path | What |
|---|---|
| `research/` | The 2026-08 research pass that preceded this design: harness architecture, local inference on Apple Silicon, Go TUI ecosystem, remote and mobile access, browser and simulator automation, benchmarks, alternatives, plus the earlier design proposal and synthesis (superseded by `DESIGN.md`) and `links.md` |
| `notes/` | Short design notes still referenced from `DESIGN.md` (below) |
| `demos/` | Re-runnable verifications of open questions, each with a README of numbers |

## Notes

| File | Feeds |
|---|---|
| `agent-lock-coordination.md` | Threads and scheduling: directory-subtree leases keyed on the write target, advisory, TTL, commit downgrades to rebase-risk. Full Go implementation in `../agent-locks` |
| `code-in-the-loop-adrs.md` | Recordings and browser: one line per ADR (deterministic replay, falsifiable expectations, deny-by-default mutation, perception parity, model tiering). Full project in `../code-in-the-loop` |
| `is-it-risky-deterministically.md` | Deferred risk scoring: capability delta, blast radius, signature change, merge-then-monitor |
| `worktrees-vs-directories.md` | Why directory identity is the isolation unit and git is orthogonal |
| `ai-dispatch-plan.md` | Mobile dispatch: Tailscale identity, HMAC envelopes, kill switch, sandbox settings |
| `merge-based-stacking.md`, `stacked-pr-review-skill.md` | Deferred VCS work: merge-forward stacks and review state that survives force-pushes |
| `hunk-review-ideas.md` | Deferred VCS work: hunk-style review from Neovim over jj |

## Demos feeding Cycles

| Demo | Establishes |
|---|---|
| `demos/fail-to-pass/` | A fix's exit condition is checkable by reverting its non-test hunks: 16 of 19 Go-touching commits over 30 are fail-to-pass, ~7 s each |
| `demos/pattern-sweep/` | The generalize phase is seedable for a local syntactic cause (4 sites, no false positives) and noise for a dataflow one (100 hits) |
| `demos/context-shape/` | 97.6% of a real transcript is re-derivable; the session's largest investigation distils to a 360-token hypothesis ledger |

## Relocated projects

| Now at | Was | Keeps in Wavez |
|---|---|---|
| `../agent-locks` | Go CLI and hooks for advisory subtree leases across agent sessions (implemented) | Design note above. The lease state machine and event-log storage are the model for Wavez's scheduler |
| `../code-in-the-loop` | Browser automation design: 14 ADRs, 11 design docs, MVP spec, threat model (research only) | ADR index above. Recordings borrow confirm/falsify expectations and compile-not-record |
| `../what-did-ai-do` | Go tool that turns agent transcripts into comprehension quizzes and an adversarial slop review (implemented) | Deferred list. Its session IR is a candidate input format for Recordings |
| `../ai-writing-styles` | Voice skill plus a Go CLI scaffold for corpus import behind a privacy-review TUI | Nothing. Unrelated to Wavez |
| `../local-code` | DuckDB plus tree-sitter hybrid code search design (research only) | Superseded by the `codegraph`-fed SQLite store in `DESIGN.md`. Its NL-to-structural-pattern idea is a Modifiers front door |
| `../ecs-logs` | Bash script for ECS task logs with a `gum` picker | Nothing. Only the habit of failing with the next command to run |

Removed from the tree (still in git history): the raw 2000-line hunk exploration transcript, `merge-based-stacking/target-vs-stacks.md` (Pulumi, off-topic).
