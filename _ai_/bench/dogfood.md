# Dogfooding wavez

Runs of `wavez -p` against real tasks in this repo, on qwen3:8b through
llama-server. Kept thin: what changed, and what the numbers argued for.

## 2026-08-16, first runs

| Task | Result | Wall |
|---|---|---|
| Read a file, summarize a type | correct | 32.5 s |
| Create a rule YAML | correct, 3 tool calls | 84 s |
| Insert 3 lines with `str_replace` | wrong, did not compile | 119 s |
| Same edit, thinking off | worse, replaced the anchor | 13.8 s |
| Same edit, thinking off + fixed schema | right placement, still did not compile | 14 s |

Three separable causes, not one.

**Thinking mode was on.** Replying "OK" cost 92 completion tokens, ~89 of them
reasoning. Decode is the bottleneck at ~21 tok/s, so every turn paid for a
reasoning trace nobody read. `--chat-template-kwargs '{"enable_thinking":false}'`
takes the same reply to 3 tokens and the edit task from 119 s to 14 s. The flag
now ships in `internal/runtime`.

**The `str_replace` schema was wrong about its own semantics.** It said
new_string was "text to put in place of old_string" without saying the
replacement is total, so the model deleted the anchor line when asked to insert
before it. Saying so plainly fixed the placement.

**What is left is not model work.** The residual failures were a missing
`path/filepath` import, a missing constant, and indentation. Imports and
formatting are what DESIGN.md assigns to deterministic resolution and to the
format pre-pass, not to the model.

Lesson: measure the harness before blaming the model. Two of the three causes
were ours, and they were worth 8.6x in wall time.

## Model A/B, same edit task

| Model | Decode | Native tool calls | Edit task |
|---|---|---|---|
| qwen3:8b, thinking on | 21.5 tok/s | yes | 119 s, did not compile |
| qwen3:8b, thinking off | 21.5 tok/s | yes | 14 s, right placement |
| qwen2.5-coder:7b | 30.9 tok/s | **no** | 10.5 s, no tool call at all |

qwen2.5-coder is faster and coder-tuned and still unusable here: its GGUF chat
template carries no tool support, so llama.cpp returns `tool_calls: null` and
the model invents `<function name=... />` XML in the content instead. Confirmed
against llama-server directly, so this is the template, not our client. It keeps
the role DESIGN.md already gives it, filling holes for intent edits, where no
tool call is needed.

qwen3:8b with thinking off stays the local default. Nothing in the 16 GB class
beats it for an agentic loop, and the tiers above it are ruled out by disk
before RAM: Devstral Small 2 wants 32 GB, Muse Glimmer is ~20 GB at 4-bit, and
the disk-streaming C engines (kimi-k3-in-c, colibri) need 167 GB to 1.7 TB and
run at 0.05 to 0.1 tok/s, roughly 500x slower than what an agent loop needs.

## ast-grep

A bare Go pattern silently matches nothing: `fmt.Println($$$ARGS)` parses as a
type conversion with an ERROR node, and the scan exits 0 having matched zero
times, which is indistinguishable from a clean pass. Call patterns need the
`context` plus `selector` form. The rule loader should reject the bare form.

## Harness fixes the runs paid for

Each of these was found by watching a real run fail, not by review.

- Thinking left on cost 30x the output tokens on short turns. 8.6x wall time
- `str_replace` never said its replacement was total, so "insert before" deleted
  the anchor line
- A failed anchor echoed a near match as long as `old_string`, so a bad anchor
  returned most of the file and paid for it twice
- `read` rejected `start_line` without `end_line`, which is what the model sends,
  wasting a turn every time
- There was no base system prompt at all. The model got tool schemas and the
  user's words, and nothing saying imports and formatting are automatic or that
  gates decide when it is done

## Where it stands

The model lands the edit when its anchor matches and cannot reliably produce a
verbatim anchor, which is DESIGN.md's measured 2/10 on this model. Every
harness-side cause found so far is fixed, so what is left is the model. The
design's own answers are the next things to test: escalate to hosted after one
failed edit, and move named changes to Modifiers and intents.

## The loop closing

A forced-broken edit now runs the whole path: the model edits, gates fail
verification, the trimmed build error comes back, the model reads it correctly
("declared but not used, and undefinedSymbol is not defined"), the thread ends
`verify_failed`, and the checkpoint operation id is on both the error event and
the abandoned-gate event. `jj op restore <id>` puts the file back and the repo
builds clean.

Three harness bugs stood between that and the earlier runs, all mine:

- `-max-turns` rebuilt the loop without the verifier, and every dogfood run
  passed that flag, so gates could never have fired
- The format pre-pass shelled out to `goimports`, which a released binary would
  never have on PATH. It runs in process now, and fails loudly when the go
  toolchain is missing rather than silently adding no imports
- A missing hosted key blocked a local-only run, because the hosted provider
  resolved its credential at construction

The model reached for `//nolint:varcheck` to silence a build error rather than
fix it. The guidance against that is in this repo's AGENTS.md, and wavez never
read it: not auto-loading agent files is a design decision, and the mechanism
that replaces it (`context` in `.wavez.pkl`, plus rules) was simply unused. Both
are wired now, and the rule catches a bare `//nolint` while allowing the house
style that carries a reason.

## 2026-08-18, diagnostics panel and model screen

One turn through the daemon (`Reply with the single word OK`, qwen3:8b,
thinking off, in a scratch jj repo since a git worktree of the main tree is not
one) and the panel filled in: 28.2 tok/s decode, 99% prefix hit on the run's
second request, 1.6k of an 8.0k window, model footprint 3.6 GB. Two probes of
the same server before that: the first request cached 1 of 18 prompt tokens at
108 tok/s prompt eval and 28.9 tok/s decode, the second cached 17 of 18 at
29.4 tok/s decode.

Two things the panel taught: RSS is the wrong number for a Metal-mapped GGUF
(16 MB against a 3.6 GB footprint), and `wavez -p` runs its own loop in
process, so nothing it does reaches the daemon's gauges.

The model screen listed both installed models with an update check against
the registry ("current" for each), previewed removing qwen2.5-coder:7b (4.4 GB
freed) and installing qwen3:4b (2.3 GB added) without acting on either, and
persisted a served-context edit and its restore. `ollama list` matched before
and after.

Three UX bugs found by driving it: `v` on Home opened an unvisited thread
onto nothing because peeking never subscribed, `Esc` inside the settings pane
left the whole screen because the global handler ran first, and a preview the
registry refused left its confirmation open. All three are fixed and tested.

## Open

- The model asserted success on code that does not compile, and nothing
  contradicted it. Gates exist but are not wired to change events
- Each `-p` run reuses one thread ID, so history accumulates across unrelated
  invocations
- One streamed token is one event, so a sentence is 30 rows in the log
- Tool inputs are not recorded in the event log, so a failed anchor cannot be
  read back after the fact. That made every str_replace failure harder to
  diagnose than it needed to be
- Hosted escalation is still unexercised, so the router's main claim is unproven

## 2026-08-18, the fix cycle on a planted bug

Setup: a scratch module in `/tmp/wz-cycle/dog` (a `lease` package copied in
shape from this repo, git plus colocated jj), with one planted boundary bug:
`Lease.Expired` returns true when `now` equals `Expires` while its doc comment
says a lease expiring exactly at now is still held. One test already existed
and passed. Command:

```
wavez -cycle fix -model local -allow-all -max-wall-clock 240s -p "Bug: in lease/lease.go, Lease.Expired reports a lease expiring exactly at now as expired, but the doc comment says ... Reproduce it with a test, then fix it."
```

Result, 2 min 22 s wall, 35 turns, 33 tool calls, $0 hosted:

```
reproduce    2 attempt(s)  artifact-fails  the change set declares no test on its changed lines, so it produced no artifact to fail
stop=condition_unmet phase=reproduce
```

Attempt 1: the model read `lease.go` once, then made 21 consecutive
`hypothesis` calls, every one marked `falsified`, none preceded by an
experiment, until the exact-repeat detector escalated and then stopped it
(`loop_detected`). It never wrote a test. Attempt 2 started from the standing
goal, the (empty) change set, and those 21 rows, called `context`, `read`,
`search` twice, `read` again, then emitted a `write` call whose input was not
valid JSON, which is `malformed_tool_call` and the end of the loop.

What the harness did right: the phase never advanced. Both verdicts are on
the cycle thread's log as `KindCycle` rows with the reason, the outcome is
`condition_unmet`, the exit status is 1, and `jj diff` shows no source change
outside `.wavez/`. A prompt describing the same three steps would have
reported the model's own account of itself as done.

What it exposed, fixed in the same change: the ledger accepted the same
(cause, verdict) row 21 times, so it now refuses a duplicate and the tool
result tells the model recording is not progress. Also worth knowing: the
hypothesis tool is a sink for qwen3:8b. Given a note-taking tool and a phase
whose exit it cannot satisfy in one edit, it takes notes. The narrowed tool
set for reproduce still includes `write` and `str_replace`, so the failure
was not the fence.

Not measured: whether the ledger-in-place-of-transcript handoff produces
work as good as a long conversation, since no run reached fix. On this model
the reproduce phase's binding constraint is the same one the M1 edit
measurements found (a valid tool call with a verbatim body), which is a
model problem the cycle correctly refused to paper over.

The TUI path was also driven under tmux: `n`, prompt, `tab`, `fix`, `enter`
creates a cycle thread; the transcript renders `▸ cycle` and `hypoth` rows.
Two findings. An unknown cycle name is refused by the daemon and the TUI
stores the error in `m.status`, which Home never renders, so it surfaces only
once a thread screen is open. And a cycle run needs a checkpoint, which needs
jj, so a git worktree that is not colocated fails the first phase at once
with `capturing checkpoint`.
