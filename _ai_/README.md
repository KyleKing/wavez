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
| `nvim-prompt-editing.md` | Plan for `$EDITOR` handoff from the composer and a `wavez lsp` completion server for prompt buffers |
| `ai-feature-surface.md` | Open decisions on Skills, hook events, a model-visible scratchpad, and the features other harnesses ship that wavez may not want |

## Demos

Only demos whose design has not shipped yet are kept. Once one becomes real code, its
directory goes and `DESIGN.md` carries the numbers.

| Demo | Establishes | Waiting on |
|---|---|---|
| `demos/code-store-python/` | coverage.py `--cov-context=test` as a line-to-test store: ~8% over plain coverage, 122 pairs into 422 range rows | A Python coverage adapter (M1 if the selection primitive settles) |
| `demos/intent-edits/` | Resolver covers 55% of added lines alone and 80% with one hole fill; intent plus hole on qwen3:8b is 3.9x faster than a hosted model writing whole files | Intent-edit resolver (M3). Its `corpus/` and `timing/` scripts also seed the benchmark harness |
| `demos/pattern-sweep/` | The generalize phase is seedable for a local syntactic cause (4 sites, no false positives) and noise for a dataflow one (100 hits) | Cycles (M2) |
| `demos/pkl-routines/` | `hk.pkl` can import `.wavez.pkl` so a hook runs the agent's routine; ~130 µs warm, 10-14 ms cold | Routines (M2) |

`bench/` is where the M3 benchmark harness writes its tables. `bench/dogfood.md` holds
the failures found running wavez against itself.

## Relocated projects

| Now at | Was | Keeps in Wavez |
|---|---|---|
| `../agent-locks` | Go CLI and hooks for advisory subtree leases across agent sessions (implemented) | Design note above. The lease state machine and event-log storage are the model for Wavez's scheduler |
| `../code-in-the-loop` | Browser automation design: 14 ADRs, 11 design docs, MVP spec, threat model (research only) | ADR index above. Recordings borrow confirm/falsify expectations and compile-not-record |
| `../what-did-ai-do` | Go tool that turns agent transcripts into comprehension quizzes and an adversarial slop review (implemented) | Deferred list. Its session IR is a candidate input format for Recordings |
| `../ai-writing-styles` | Voice skill plus a Go CLI scaffold for corpus import behind a privacy-review TUI | Nothing. Unrelated to Wavez |
| `../local-code` | DuckDB plus tree-sitter hybrid code search design (research only) | Superseded by the `codegraph`-fed SQLite store in `DESIGN.md`. Its NL-to-structural-pattern idea is a Modifiers front door |
| `../ecs-logs` | Bash script for ECS task logs with a `gum` picker | Nothing. Only the habit of failing with the next command to run |

## Removed from the tree

Still in git history, readable at the commit named beside each. Eight demos were deleted
once the design they argued for shipped as code; `DESIGN.md` keeps every number they
produced.

| Was | Shipped as | Last in |
|---|---|---|
| `demos/code-store-go/` | `internal/gate/coverage_adapter.go`, `internal/gate/importgraph.go` | `66a2092d` |
| `demos/context-shape/` | `internal/thread/ledger.go`, fork inheriting the change set | `66a2092d` |
| `demos/daemon-tui/` | `internal/daemon`, `internal/eventlog`, `internal/tui` | `66a2092d` |
| `demos/edit-loop/` | `internal/edit/replace.go`, `internal/tools/str_replace.go` | `66a2092d` |
| `demos/fail-to-pass/` | `internal/gate/failtopass.go` | `66a2092d` |
| `demos/local-models/` | `config.DefaultLocalModel`, `internal/router` | `66a2092d` |
| `demos/local-runtime/` | `internal/runtime` | `66a2092d` |
| `demos/sandbox/` | `internal/sandbox` | `66a2092d` |

Also gone: the raw 2000-line hunk exploration transcript, and
`merge-based-stacking/target-vs-stacks.md` (Pulumi, off-topic).
