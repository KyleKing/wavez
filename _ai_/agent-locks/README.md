# agent-locks

Advisory coordination for parallel coding agents sharing one working tree.

Several Claude Code sessions (plus me, editing by hand) write into the same repository.
Agents write across directories by absolute path, so a session's working directory says
nothing about where it actually writes, and I lose track of who is touching what.
`agent-locks` records which subtree each session is writing to and tells the next agent
that arrives.

It never blocks. A `PreToolUse` hook returns `additionalContext` naming the other
session and stays out of the permission decision entirely.

## Why it looks like this

**Keyed on the write's target path, not the session's `cwd`.** A guard keyed on `cwd`
both misses collisions and flags correct writes, because sessions legitimately write
into other trees by absolute path.

**Directory subtree, not individual files.** Measured against my own transcripts,
directory granularity catches roughly twice the adjacency that file granularity does
(241 collisions vs 109 over the same corpus). Two agents in the same package are a
problem before they touch the same file.

**Advisory, never a block.** A hook cannot sleep and wait without stalling the session,
so "wait for the other agent" is not implementable as a pause. Denying pushes agents
into workarounds. Telling them and letting them decide keeps them out of thrash loops.

**No `permissionDecision` is ever emitted.** Returning `"allow"` from `PreToolUse` would
bypass the normal permission prompts for every matching edit, which is a wider grant
than this needs. The hook only adds context.

**Fail open everywhere.** Bad stdin, missing state, a panic, or an unreadable log all
exit 0 and say nothing. A coordination tool that breaks the session is worse than no
coordination tool.

## Install

```sh
make install                # builds to ~/.local/bin/agentlocks
agentlocks install          # prints the settings.json snippet to merge
```

State lives in `~/.claude/agent-locks/`, overridable with `AGENT_LOCKS_DIR`. Tuning:
`AGENT_LOCKS_TTL` (default 30m) and `AGENT_LOCKS_COOLDOWN` (default 10m, how long
before the same pairing is mentioned again).

## Commands

```sh
agentlocks analyze --window 10m --repos-only   # mine transcripts for real collision rates
agentlocks status                              # who holds what right now
agentlocks claim ./internal/api -m "refactor"  # hold a subtree yourself
agentlocks release ./internal/api
```

`analyze` is read-only and needs nothing installed, so it is the honest first step:
run it before deciding whether the hook is worth wiring up.

## What the corpus said

Across 659 transcripts, counting only writes that landed inside a git repository:

| | |
|---|---|
| writes found | 7,600 |
| through Edit/Write | 5,999 |
| through Bash (heuristic) | 1,601 (21%) |
| from subagents | 3,086 |
| outside the session's own cwd | 517 (6.8%) |
| directory-level collisions within 10m | 323 |
| file-level collisions within 10m | 148 |
| directories touched by 2+ sessions | 201 |

Two findings shaped the build. A fifth of writes never touch the file tools, so a hook
matched only to `Edit|Write` has a real hole in it, which is why `Bash` is matched too.
And directory-level collisions run about 2.2x file-level, which is the case for subtree
granularity.

## Lease lifecycle

A subtree lease is `active` while a session has written there recently. A `git commit`
detected in `PostToolUse` downgrades every lease under that root to `committed`, where
the message changes from "being edited right now" to "rebase risk". A TTL expires
anything left behind by a crashed session. Manual claims never expire until released.

In a shared checkout, "committed" is a property of the tree rather than of a session, so
attribution comes from the event log and git state only supplies the release condition.
Push-based release would only be meaningful per worktree and is not implemented.

## Storage

An append-only `events.jsonl` plus a compacted `state.json`. Records stay small enough
that `O_APPEND` makes concurrent writes from independent sessions atomic without a lock,
and compaction takes an exclusive `flock` that any session may skip. The log stays
readable with `jq`, which matters more than query speed at this size.

## Limits

- The context arrives with the tool result, so a session learns about contention as its
  first write lands, not before. Every write after that is informed. This is inherent to
  the advisory posture: the alternative is denying the call
- Bash write detection is a heuristic. It prefers false negatives, and it will still
  miss codegen invoked through a task runner
- Reads are not tracked at all, so a session about to edit a file another session is
  studying gets no signal
- Only Claude Code is wired up. The event schema is agent-agnostic, but nothing emits
  into it from opencode or gemini-cli yet
- No TUI. `status` prints the current picture and that is the whole live view for now

## Testing

```sh
make test        # unit tests for the detector and lease grading
make demo        # drives the hook with synthetic stdin, asserts what the model sees
```

`scripts/demo.sh` simulates two sessions colliding in one subtree, a subagent as a
distinct actor, a Bash formatter write, a manual claim and release, a commit downgrade,
session end, and malformed input. It uses a scratch state dir and scratch repo.
