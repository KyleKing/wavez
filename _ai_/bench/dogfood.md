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
