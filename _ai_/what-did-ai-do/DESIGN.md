# Design

<!-- Project-specific architecture, design decisions, and domain context.
     This file is preserved across template updates (_skip_if_exists). -->

## Problem

AI coding agents (Claude Code, Aider, etc.) make decisions on the user's behalf that
never get reviewed for comprehension. Code review checks the diff is correct; it
doesn't verify the accountable human understood *why*. This tool parses local agent
session transcripts, generates active-recall quiz questions from what actually
happened, and re-quizzes on a spaced-repetition schedule so the user builds and
retains real understanding of their own codebase instead of trusting a black box.

## Architecture

```
                 ┌───────────────┐
 session files → │  Adapter      │  (per-agent: Claude Code, Aider, ...)
                 │  (parses to   │
                 │  Session IR)  │
                 └───────┬───────┘
                         ▼
                 ┌───────────────┐
                 │ Session IR    │  agent-agnostic: messages, tool calls,
                 │ (internal)    │  file diffs, timestamps
                 └───────┬───────┘
                         ▼
                 ┌───────────────┐
                 │ Decision      │  heuristic pass (tool sequence, files
                 │ Extractor     │  touched, commands run) → falls back to
                 │               │  an LLM pass only when rationale isn't
                 │               │  stated anywhere in the transcript
                 └───────┬───────┘
                         ▼
                 ┌───────────────┐
                 │ Quiz Generator│  recall questions (structural facts) +
                 │               │  rationale questions (why X over Y)
                 └───────┬───────┘
                         ▼
                 ┌───────────────┐
                 │ SRS Scheduler │  SM-2/FSRS-style review queue
                 └───────┬───────┘
                         ▼
                 ┌───────────────┐
                 │ TUI           │  Bubble Tea; runs over all local
                 │               │  sessions across projects by default
                 └───────────────┘
```

### Package structure

```
internal/
├── session/       # Session IR types (agent-agnostic)
├── decision/      # Decision IR shared by extract/quiz/adversarial
├── adapter/
│   ├── claudecode/ # parses ~/.claude/projects/**/*.jsonl
│   └── aider/       # parses .aider.chat.history.md (markdown prose, no tool-call schema)
├── extract/        # decision extraction (heuristic only, no LLM/network calls)
├── quiz/           # question generation from extracted decisions
├── srs/            # spaced-repetition scheduling (SM-2, not yet persisted)
├── discover/       # combines adapter session discovery into one sorted list
├── gitstate/       # working-tree re-resolution: is a decision's file still live?
├── llm/            # judgments via the local `claude` CLI (no separate API key)
├── adversarial/    # "AI slop" detection: batches a session's decisions into
│                    # one llm.Client.Judge call, filters by gitstate + confidence
├── findingsstore/  # persists/renders adversarial reports for the CLI and TUI
└── tui/            # Bubble Tea v2 app: session list → quiz screen → findings screen
```

### Why two adapters from day one

Claude Code and Aider were chosen deliberately, not arbitrarily. Claude Code writes
structured JSONL (tool calls, messages, results — machine-first). Aider writes
`.aider.chat.history.md`, markdown prose with no tool-call schema at all. Building
the second adapter against a genuinely different serialization format is what tests
whether the `Session` IR abstraction actually generalizes, versus a same-shaped
adapter (e.g. OpenAI Codex CLI, whose JSONL is structurally near-identical to Claude
Code's) that would just be a reskin and teach nothing about the abstraction's limits.

## Patterns

- Adapter interface: `func Parse(path string) (session.Session, error)` per agent;
  adding a third agent is a new adapter package, not a change to downstream stages.
- Decision extraction stays heuristic-first; the LLM fallback is invoked lazily and
  only for genuinely ambiguous decision points, to bound cost.
- The TUI reads pre-generated structured JSON (session → decisions → quiz) rather
  than doing extraction inline, so a future thin web dashboard can read the same
  artifacts without duplicating logic.

## Non-goals for MVP (deferred, not forgotten)

- **Natural-language search over session history.** Already solved by existing OSS
  (cccmemory, cc-recall, Session Search MCP skill). Integrate one of these later for
  an "ask about past sessions" feature rather than rebuilding retrieval.
- **Duplicate/redundant session detection.** Compare tool-call sequences and diffs
  across sessions to flag when the same task is being repeated, and suggest
  automating the repeated work into a script, CLI tool, or Skill to save tokens.
  Mostly structural (sequence similarity over the Session IR), so cheap to add once
  the IR and multi-session storage exist, but out of scope until the core quiz loop
  is validated.
## Adversarial decision-quality analysis (shipped)

A pass that flags AI decisions that look like "AI slop" — hasty, unjustified, or
clearly suboptimal changes — surfaced as a learning opportunity, not a bug report.
Opt-in via `what-did-ai-do review [--session ID]`, not run automatically during quiz
generation, because it requires a real LLM judgment call per session (batched: every
decision in a session goes into one `llm.Client.Judge` call, not one call each, since
a single call carries a fixed system-prompt/context cost of roughly $0.02 — batching
is what keeps analyzing a whole session affordable).

- **LLM backend**: the local `claude` CLI (`internal/llm`), not a standalone
  `ANTHROPIC_API_KEY`. Shells out to `claude -p --model claude-haiku-4-5
  --output-format json --json-schema ... --append-system-prompt ...`, reusing
  whatever login the user already has. `--bare` is deliberately avoided: it strictly
  requires an API key and bypasses OAuth/keychain login, defeating the point.
  Anthropic's tool-calling API requires a `--json-schema`'s root type to be
  `"object"`, not `"array"` (confirmed by hitting the actual 400 error), so the
  schema wraps the judgments array in one field and `llm.Judge` unwraps it.
- **Current-state re-resolution** (`internal/gitstate`): compares a decision's
  captured "new" text against the file as it sits in the **working tree right now**
  (not git HEAD — uncommitted changes count). Three-state outcome: live (still
  present, worth judging), superseded (rewritten since — skip the LLM call
  entirely, nothing to flag), gone (deleted, with best-effort git rename detection).
  Degrades to `unknown` rather than erroring when the project isn't a git repo or
  git isn't installed.
- **Conservative by design**: only "questionable"/"slop" verdicts at confidence
  ≥ 0.8 are surfaced (`minConfidenceToFlag` in `internal/adversarial/prompt.go`).
  A feature that cries wolf erodes trust faster than one that misses a few real
  issues. The system prompt embeds a small few-shot **golden example set**
  (`internal/adversarial/examples.json`) distinguishing genuine slop (destructive
  fixes with no diagnosis, bypassed safety checks) from decisions that only look
  unjustified (a mechanical rename with no rationale is fine; missing commentary
  alone is never sufficient to flag). The same file backs an opt-in regression test
  (`WDAI_LLM_TESTS=1 go test ./internal/adversarial/... -run TestGoldenExamples`)
  against the real CLI, so the rubric can be tuned over time without silently
  drifting.
- **Surfacing**: a written report (`findingsstore.Render`, shared by the CLI command
  and the TUI), a cached findings screen in the TUI (press `f` on a session that's
  been reviewed), and a session-list badge when findings exist. A "what would you
  have done differently?" quiz question type is scoped but not built — it should
  wait until flagging precision is validated in practice, otherwise it quizzes users
  on the model's own hallucinated regrets.
- **Not yet built**: duplicate/redundant session detection (see below) and any
  automatic/inline invocation of adversarial analysis during normal quiz use.

## Non-goals for MVP (deferred, not forgotten)

- **Natural-language search over session history.** Already solved by existing OSS
  (cccmemory, cc-recall, Session Search MCP skill). Integrate one of these later for
  an "ask about past sessions" feature rather than rebuilding retrieval.
- **Duplicate/redundant session detection.** Compare tool-call sequences and diffs
  across sessions to flag when the same task is being repeated, and suggest
  automating the repeated work into a script, CLI tool, or Skill to save tokens.
  Mostly structural (sequence similarity over the Session IR), so cheap to add once
  the IR and multi-session storage exist, but out of scope until the core quiz loop
  is validated.

## Testing

<!-- Document project-specific testing strategies, mocking patterns, and coverage targets here. -->
