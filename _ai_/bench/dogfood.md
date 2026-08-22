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
