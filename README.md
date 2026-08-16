# Wavez

A personal AI coding agent built for one user, one laptop, and repeated narrow work. It spends fewer tokens by doing the predictable parts of coding deterministically (which tests to run, how to rename a symbol, what to strip from context) and reserves the model for the parts that need judgment.

Status: design phase. [DESIGN.md](DESIGN.md) holds the architecture, screens, per-feature requirements, decisions, and phases. `_ai_/` holds the half-finished projects and research this consolidates.

## What it does differently

- **Structural rules** (`ast-grep` embedded, Semgrep CE opt-in) enforce project conventions as YAML instead of prose, gate every edit, drive codemods and autofix as Modifier calls, and flag new capabilities in a change set
- **Gates** run checks in response to what changed. The model never decides what to test. Coverage map plus changed lines selects the test subset. Format and lint run as pre-passes. Passing gates return nothing to the model. Failures return only the failing test names and the frames that touch changed files
- **Routines** are user-defined workflows in pkl with workflow-engine semantics (DAG steps, concurrency keys, cancel-in-progress, multiple triggers) that run locally with resource locks. Gates are the built-in routines
- **Edits** are line-anchored ops (replace, insert, delete by content-hashed line id) so the model emits an address and new lines, never the old text, and stale edits are rejected before writing. Hosted models keep their native formats
- **Intent edits** (exploration) go further: the model or a human states `add fn parseTTL(cfg Config) time.Duration near TTL` or `like Foo: add Bar`, and a resolver driven by the code-intelligence store places the code, adds imports, plumbs config, registers routes, writes the test stub, and leaves a small local model only the hole that structure cannot decide. The same resolver is a completion source in Neovim
- **Modifiers** let the model call a refactor engine (rename, move, extract, add import, stub from signature) with a dozen tokens instead of emitting the edited text. Backed by LSP, `gopls`, `ast-grep`, `ts-morph`, and `rope`
- **Threads** replace the single god session. Each work stream has its own compacted history. A scheduler coordinates threads that touch the same directories, alternates edit and execute phases, and respects the laptop's memory
- **Code intelligence** is one SQLite store per project (symbols, call and import edges, trigram FTS, embeddings, line-to-test coverage, cross-stack contracts) fed incrementally by tree-sitter, `codegraph`, coverage adapters, and a small local embedder. One `search` tool with fuzzy, semantic, graph, and hybrid modes, and one `context` bundle (repo map plus one-hop neighbourhood plus covering tests) for a small model's first turn. Everything else reads it: gates, modifiers, intent edits, similarity notes, the scheduler, Neovim pickers
- **Compaction** is deterministic first (append-only trimming that keeps the prompt-cache prefix stable, rule-based stdout truncation, tool results dropped after N turns) and model-based only for the residue
- **Recordings** capture PTY and browser step sequences as they happen so a fix can be replayed for regression, then promoted to a test or discarded

Also: local models first (chosen for tokens/sec on this laptop), hosted models through OpenRouter when a task needs more, works across directories rather than worktrees, one pane of glass across concurrent agents, macOS Seatbelt sandbox plus a destructive-command guard, and a daemon/TUI split so a phone client can attach later.

TLDR: fewer tokens, faster builds, higher quality, low RAM.

## Phases

| Version | Usable for | Adds |
|---|---|---|
| v0.1 | Single-thread edits on one project, replacing Claude Code for small tasks | TUI (home, thread, inbox), chat loop, code-intelligence store with `search` and `context`, Gates, sandbox + permission gate, local model + OpenRouter escalation |
| v0.2 | Several concurrent threads across directories | Routines (pkl, DAG runner, locks), Threads dashboard, scheduler, PTY recordings, semantic index, similarity notes, repo map |
| v0.3 | Cheaper and faster on the same work, from Neovim too | Modifiers, intent edits, deterministic compaction, cross-stack contract nodes, `wavez.nvim`, web search |
| v0.4 | Away from the laptop | jj/git integration layer, mobile client (Tailscale + PWA + push) |
| v0.5 | Proving it | Browser recordings, benchmark harness against Claude Code / OpenCode |

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

- Whether a stronger local coder than qwen3:8b exists that fits 16 GB, or whether multi-file edits always go hosted
- How much of the session ledger needs a model-written handoff note versus structural facts alone
- Whether hashed-line edit ops beat `str_replace` on an 8B local model (malformed rate, tokens, latency)
- How to keep the per-test coverage map incremental across branches and rebases without a full 4-minute rebuild
- Web search: which API, how to keep results current for the right software version, whether Dash docsets cover enough
