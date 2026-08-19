# Wavez

A personal AI coding agent built for one user, one laptop, and repeated narrow work. It spends fewer tokens by doing the predictable parts of coding deterministically (which tests to run, how to rename a symbol, what to strip from context) and reserves the model for the parts that need judgment.

Status: M2 in progress. [docs/glossary.md](docs/glossary.md) is the map: the vocabulary, how a turn and a cycle run, and the decisions worth knowing before reading code. [DESIGN.md](DESIGN.md) holds the architecture, screens, per-feature requirements, decisions, and milestones. `_ai_/` holds the half-finished projects and research this consolidates.

![Home, a thread, the schedule, and diagnostics in the wavez TUI](docs/demo.gif)

`docs/demo.tape` records the tour above against a scratch daemon with no model attached (`mise run demo` re-renders the gif and the stills under `docs/img/`).

## What it does differently

- **Gates** run checks in response to what changed, so the model never decides what to test. The coverage map plus the changed lines picks the test subset, format and lint run as pre-passes, a passing gate returns nothing, and a failing one returns the failing test names and the frames touching changed files
- **Cycles** are phased ways of working (the built-in fix cycle is reproduce, fix, generalize) where a phase advances only when the harness observes its exit condition (a failing test, that test passing with green gates, a structural sweep with every hit accounted for), never when the model says it is done. `wavez -p "…" -cycle fix` runs one, and a phase that cannot satisfy its condition ends the cycle with that reason rather than as complete
- **Routines** are user-defined pkl workflows with workflow-engine semantics (DAG steps, concurrency keys, cancel-in-progress, several triggers) run locally under resource locks. Gates are the built-in routines
- **Modifiers** (M3) let the model call a refactor engine (rename, move, extract, stub from signature) with a dozen tokens instead of emitting the edited text. **Intent edits** (M3) go one step further: `add fn parseTTL(cfg Config) time.Duration near TTL` or `like Foo: add Bar`, and a resolver places the code, adds imports, registers routes, and writes the test stub, leaving a small local model only the hole structure cannot decide
- **A dashboard, not a chat app.** Home lists threads across repos the way gh-repo-dashboard lists repos, an inbox collects every prompt that needs you, a schedule view shows lanes and locks, and a diagnostics panel shows memory, model state, cache hit rates, gate latency, and lease contention live. Vim-shaped controls layered from arrows to `:` verbs
- **Threads** replace the single god session. Each work stream carries its own compacted history, and a scheduler coordinates threads touching the same directories against the laptop's memory
- **Code intelligence** is one SQLite store per project (symbols, edges, trigram FTS, line-to-test coverage) that every other subsystem queries: gates today, and modifiers, intent edits, and the Neovim pickers as they land

Also: local models first (qwen3:8b on `llama-server` with n-gram speculation, chosen by measurement on this laptop, or a `llama-server` on another machine over Tailscale), hosted models through OpenRouter when a task needs more, works across directories rather than worktrees, macOS Seatbelt sandbox plus a destructive-command guard, and a daemon/TUI split so a phone client can attach later.

TLDR: fewer tokens, faster builds, higher quality, low RAM.

macOS only for now. The sandbox is a Seatbelt profile and the symbol indexer is cgo tree-sitter, so neither cross-compiles nor has a Windows or Linux equivalent yet. Both sit behind interfaces, so a port is a contribution the design already has room for rather than a rewrite. Contributions welcome.

## Milestones

What each milestone ships and the condition that closes it are in
[DESIGN.md](DESIGN.md#milestones).

| Milestone | Usable for |
|---|---|
| M1 Loop | Single-thread edits on one project, replacing Claude Code for small tasks |
| M2 Fleet | Several concurrent threads across directories |
| M3 Cheaper | Cheaper and faster on the same work, from Neovim too |
| M4 Away | Away from the laptop |
| M5 Proof | Proving it against Claude Code and OpenCode on the same tasks |

Innovation tokens go to Routines + Gates and Modifiers. Everything else copies prior art (Crush for the Go tool loop and Bubble Tea patterns, opencode for compaction policy, `_ai_/` projects for locks, risk scoring, and browser safety).

## Non-goals

- Serving more than one user or trust level (no config hierarchy, no policy layer, no telemetry export)
- Plugins or extensibility. Tools are built in and opinionated
- Auto-loading `AGENTS.md`, `CLAUDE.md`, or `.agents/`. Context is listed explicitly in `.wavez.pkl`
- Replacing herdr, tmux, or an editor. Wavez owns the agent loop and the checks around it
- Multi-agent hierarchies past one level of delegation

## Prior art

[`_ai_/README.md`](_ai_/README.md) indexes the research pass, the design notes `DESIGN.md` leans on, the demos, and the projects relocated to sibling directories (`../agent-locks`, `../code-in-the-loop`, `../what-did-ai-do`, `../local-code`).

## Open questions

The list lives in [DESIGN.md](DESIGN.md#open-questions). The two that decide whether the
thesis holds: whether a local coder stronger than qwen3:8b fits 16 GB, and whether an 8B
model fills an intent-edit hole correctly with retry against gates.
