# Soft locks for parallel agents in a shared checkout

Notes and a decision record for coordinating multiple Claude Code sessions (plus me,
editing by hand) writing into one working tree.

## The problem

Agents are allowed to write across directories, so a session's working directory tells
you nothing about where it actually writes. Several sessions plus manual edits land in
the same tree, agents get blindsided by changes they did not make, and I lose track of
which session is active where. Worktree-per-session (the official answer) does not fix
this, because agents write into other trees by absolute path anyway.

The one hard number I found on this comes from
[anthropics/claude-code#76727](https://github.com/anthropics/claude-code/issues/76727),
where someone mined 30 days of transcripts across 15-20 concurrent sessions: of 13,782
Edit/Write calls, 44% wrote into the shared primary checkout and 29% wrote into a
worktree by absolute path. Their conclusion is the constraint everything else follows
from: key coordination on the **write's target path**, never on the session's `cwd`.

## Decisions

**Advisory only, never blocking.** A `PreToolUse` hook returns
`permissionDecision: "allow"` plus `additionalContext` naming the other session and what
it touched. A hook cannot sleep-and-wait without stalling the session for up to its
timeout, so "wait for the other agent" is not implementable as a pause. The realistic
choices are block-with-a-hint or tell-and-proceed, and tell-and-proceed keeps agents out
of thrash loops where a denial pushes them to invent a workaround.

**Directory subtree is the unit.** Claim `./internal/api` and everything under it. This
matches how I reason about who is working on what, keeps a live view readable, and catches
near-miss collisions (two sessions in the same package, different files) that file-level
locks miss. The cost is false contention on wide directories.

**Both tiers of surface exist.** My own manual claims and agent leases live in the same
registry so contention between me and an agent shows up the same way as agent-to-agent.

### Lease lifecycle

Roughly the model I sketched (release the fine-grained hold on commit, keep a soft
directory signal until pushed), with one wrinkle worth stating up front.

A lease passes through three strengths:

- **active** while a session has written into the subtree recently and those writes are
  uncommitted. This is the strong signal, the one that produces `additionalContext`
- **committed, unpushed** downgrades to a weak signal. The collision risk is no longer
  concurrent-edit, it is rebase and history churn, so the message changes to match
- **released** once the work is pushed, or once a wall-clock TTL expires

The TTL backstop stays regardless of git state, because a crashed session leaves both a
dirty tree and a stale lease, and nothing else will clean it up.

The wrinkle: in a *shared* checkout, "committed" and "pushed" are properties of the tree
and branch, not of a session. A commit by session A sweeps up session B's uncommitted
work, and `git status --porcelain <dir>` cannot tell you whose changes those are.
Attribution has to come from the hook's own write log (which session wrote which path,
when), with git state supplying only the release condition. Push-based release is only
meaningful per worktree, where each session owns a branch. In the shared tree, treat
commit as the release event and drop the push tier.

## Prior art

| | Enforcement | Storage | Human view | Fit |
|---|---|---|---|---|
| [preclaim](https://www.npmjs.com/package/preclaim) | Hooks. Hard `deny` on write conflict, advisory signal on reads | Hosted Supabase, one network call per write | CLI (`sessions`, `logs`), hosted dashboard | Wrong on both axes I picked (denies, per-file) |
| [mcp_agent_mail](https://github.com/Dicklesworthstone/mcp_agent_mail) | MCP tools the agent must choose to call, plus a pre-commit guard | SQLite + Git, local | 15-screen TUI, web UI | Right storage and surfaces, enforcement is voluntary |
| Worktree isolation (Claude Squad, Nimbalyst/Crystal, Conductor, vibe-kanban) | Physical separation | n/a | Per-tool dashboards | Does not address cross-tree absolute-path writes |
| Build on this repo's Go hook | Hooks on both file tools and Bash | Local SQLite | Bubble Tea TUI | Covers the Bash gap, all of it is work |

Two things nobody covers well. Bash writes (`sed -i`, formatters, codegen, `go run`
scripts) bypass the file tools entirely, so a file-tool-only hook has a hole in it. And
none of these model a *human* holding a claim alongside the agents.

Hook capability, verified against current docs, is better than issue #76727 implies:
`PreToolUse` gets `tool_input.file_path`, supports path filters in the `if` field
(`Edit(src/**)`), can return allow/deny/ask/defer plus `additionalContext`, and
`SessionStart`/`SessionEnd`/`SubagentStart`/`SubagentStop` cover registry lifecycle.
Identity needs care: subagent hooks carry the **parent's** `session_id`, so `agent_id`
is required to tell subagents apart.

## What preclaim actually is

Not open source. `github.com/paulveth/preclaim` is private or deleted (404, and the
author's two public repos are unrelated). The npm packages `preclaim` and
`@preclaim/core` are public and carry **no license field at all**, so there is no grant
to copy, fork, or vendor any of it. Published in a burst of 21 versions between
2026-03-17 and 2026-03-22, nothing since.

The shipped `dist/` is unminified TypeScript output, so the behavior below is read off
the published tarball rather than the marketing site.

```sh
npm i -g preclaim
preclaim init             # auth, project creation, writes .claude/settings.json hooks
preclaim sessions         # active sessions
preclaim logs
preclaim unlock <path> --force
```

`init` installs three hooks, all with an empty matcher so they fire on every tool call:
`PreToolUse` (the gatekeeper), `PostToolUse` (commit detection, releases locks), and
`SessionStart` (registration plus heartbeat). Back up `.claude/settings.json` first.

Defaults: 30 minute TTL, `failOpen: true`, ignore list of `*.md`, `package-lock.json`,
and `*.test.ts`.

How it behaves on a write, from `dist/hooks/pre-tool-use.js`:

- `Read` registers a soft interest, capped at 200ms, fire-and-forget
- `Edit`/`Write`/`MultiEdit` claim the lock with a 2000ms cap, in parallel with a check
  for other sessions' read interests
- `acquired` or `already_held` returns `allow` with a `systemMessage`
- `conflict` returns **`deny`**, naming the holding session, when it was acquired, when
  it expires, and the `preclaim unlock --force` escape hatch
- network failure returns `allow` with a warning when `failOpen` is set, and the whole
  hook is wrapped in a catch-all that returns silently

So the marketing framing of advisory locks applies to reads only. Writes are a hard
block. That is the opposite of the posture I chose, and it is per file with no directory
rollup, which is the opposite of the granularity I chose.

**The privacy concern was smaller than I assumed.** The payload is the path made relative
to the project root, plus `project_id` and `session_id`. No file contents, no absolute
paths, no repo identity beyond an opaque project id. What still leaves the machine is the
shape of the tree and every filename an agent touches, going to a Supabase instance
(`aawbukcvngdffueowjsa.supabase.co`) run by one person, with the anon key hardcoded in
`config.js`. For a private work repo that is a judgment call rather than an automatic no.

**A self-hosted backend is mechanically possible and not permitted.** `config.backend` is
a configurable base URL, `PRECLAIM_SUPABASE_URL` and `PRECLAIM_SUPABASE_ANON_KEY` are
honored, `http://localhost` appears in the code, and the whole API surface is
`/api/v1/locks` and `/api/v1/onboard`. Small enough to reimplement in an afternoon. With
no license on the client, reimplementing the server to point a licenseless client at it
is not a footing I want to build on.

### What it validates

Worth taking from it regardless of whether I run it: commit-as-release is implemented in
`PostToolUse` and evidently works well enough to ship, which supports the lifecycle
model above. The two-tier split of hard claims on writes and cheap soft signals on reads,
with a tight timeout on the soft path, is a good shape. And fail-open is treated as
non-negotiable, wrapped at three separate levels.

### Verdict

Not worth installing as a trial. It denies where I want context, locks per file where I
want subtrees, has no notion of a claim I hold by hand, routes every write through a
third party's hosted database, and has been dormant for four months with no license.
The parts I wanted to learn from, I have now read.

## How to validate any of this

Measure before building. The transcript archive under `~/.claude/projects/**/*.jsonl`
already holds the answer and costs nothing to mine.

1. **Baseline the collision rate.** Extract every Edit/Write/NotebookEdit call with its
   `file_path`, `session_id`, `agent_id`, and timestamp. Bucket by directory. Count pairs
   where two distinct sessions wrote into the same subtree inside a 10-minute window.
   Repeat at file granularity. If the directory-level number is large and the file-level
   number is near zero, coarse locks would be mostly false alarms and the granularity
   decision needs revisiting
2. **Find the Bash hole.** Same archive, Bash tool calls, matched against patterns that
   write (`>`, `>>`, `sed -i`, `tee`, `mv`, formatters, generators). If this is a
   meaningful fraction of writes, a file-tool-only hook is not worth shipping alone
3. **Check whether git state is a usable release signal.** For the collisions found in
   step 1, how long did the tree stay dirty in that subtree? If sessions rarely commit,
   commit-based release never fires and it degenerates to a plain TTL
4. **Does the model act on advisory context?** The only test is live. Run the hook in
   log-only mode (emit the `additionalContext` and record it, change nothing), then read
   transcripts for whether agents that received a contention notice changed course. If
   they routinely ignore it, advisory-only was the wrong call and the choice is between
   `ask` and accepting the collisions
5. **Does it help *me*?** The point is partly a live view I can trust. That is not
   measurable from transcripts, it needs a week of use

Steps 1 through 3 are a scripting afternoon against data that already exists, and they
decide granularity, scope, and release semantics. Do those before writing a hook.

## Built

`~/Developer/local-code/agent-locks`. The measurement steps ran first and their results
are recorded in that README. Two of them changed the build: a fifth of writes never
touch the file tools (so `Bash` is matched too), and directory-level collisions run
about 2.2x file-level (which supports subtree granularity over per-file).

One design choice diverged from the plan above. The hook emits no `permissionDecision`
at all rather than `"allow"`, because returning `"allow"` from `PreToolUse` suppresses
the normal permission prompt for every matching edit. That is a wider grant than
coordination needs, and `additionalContext` alone carries the advisory message.

## Open questions

- Whether the TUI should read the registry directly or shell out, given it should
  eventually cover opencode and gemini-cli (Claude Code only for now)
- How manual claims get created: a CLI, a keybinding in the TUI, or both
- Whether read-interest signals (Read/Grep) are worth tracking or just noise at
  directory granularity
- What happens on `/clear` and resume, where `SessionEnd` fires but the session continues
