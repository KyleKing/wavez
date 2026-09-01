# 2026-08-31: driving the TUI under a PTY, two lanes at once

A scratch `wavezd` on `/tmp/wzdog.sock` over this checkout, the TUI under
`tmux new -d -s wz -x 140 -y 42`, and threads created with `n` and driven
with `Enter`. Every number below is read from `.wavez/threads/<id>.jsonl`
rather than from the screen.

## What the lanes did

| lane | task | end | turns | spend |
|---|---|---|---|---|
| `b7700e71ac4c6d10` | `wavezd` announces the socket before `Serve` binds | done | 23 | $0.14 |
| `af940427c417b9b6` | `llama-server` starts with no `--cache-ram` | failed | 8 | $0.05 |

Both changes build and pass `go test ./cmd/wavezd/ ./internal/daemon/
./internal/runtime/`. The daemon lane split `Server.Bind` out of
`Server.Serve`, made it idempotent, left `Serve` binding for itself when
nobody bound first, and moved the announcement in `cmd/wavezd/main.go` after
the bind. The runtime lane added `Config.CacheRAMMiB`, a
`DefaultCacheRAMMiB` of 512, and `--cache-ram` on the command line.

## The bug that started it

`cmd/wavezd/main.go` printed `wavezd listening on <sock>` before
`srv.Serve(ctx)` bound anything, so a bind failure printed success and then
the error. Reproduced by pointing the daemon at a socket path in the
scratchpad: 119 bytes against the 103-byte unix limit, and the run printed
the claim first.

## Parallel lanes share one gate and one tree

The runtime lane's change set was abandoned for a failure it did not cause.
Its verification round ran the daemon lane's in-flight test, which was at
that moment failing:

```
go-test TestServeNoListeningClaimWhenBoundElsewhere
  no output line named a changed file. What it printed:
      main_test.go:27: occupying socket: listen unix ...: bind: invalid argument
```

The lane had been told not to touch `cmd/wavezd` or `internal/daemon`, and
the gate handed it a failure in exactly those. Before that it read
`internal/daemon/daemon.go` to diagnose a build error that was the other
lane mid-`str_replace`. This is DESIGN's write-fence item with an instance.
What needed coordinating was the tree and the verifier the two lanes shared.

## The header names a model and a window that did not serve the turn

Every one of the 23 turns in `b7700e71ac4c6d10` and all 15 in
`af940427c417b9b6` carry `Detail["tier"] = "balanced"` and
`Detail["model"] = "glm-5.3"`. The header read `qwen3:8b 9.4k/8.2k`
throughout. Both halves are wrong for the same reason: `activeModel` treats
an absent `Override` as "served by the daemon's local model", and
`managedThread.window()` returns the fast tier's budget whatever answered.
The gauge therefore shows a 9.4k prompt overflowing an 8.2k window while the
turn ran in a 128k one.

## Live spend is invisible

`mt.spendUSD` is only incremented from a run's outcome, so a thread reads
`$0.00` for its whole run and reports a number once it stops. The runtime
lane showed `$0.00` until it failed, then `$0.05`. On a metered tier the one
number that would justify stopping a run early is the one withheld until it
is over.

## The local server served nothing

`wavezd` started `llama-server` with the ollama blob for `qwen3:8b`, held it
resident for the whole session, and the router sent every turn of every lane
to the hosted balanced tier. Startup also spent about 30 seconds silent
while the model loaded, with no output before the listening line.

## A new lane inherits the working copy and is blamed for it

The third lane was given the header bug above, with the thread ids and the
`Detail["tier"]` evidence in its prompt. It reported `done` after 3 turns
and $0.02 having never opened `internal/tui`. Its first event after the
prompt is a gate failure, and the feedback names the previous two lanes'
files:

```
Gates ran on your changes and found this:
  lint lint
    cmd/wavezd/main_test.go:34:2: G104: Errors unhandled (gosec)
    internal/daemon/daemon.go:369:3: ineffectual assignment to ln (ineffassign)
    internal/daemon/daemon.go:358:2: directive `//nolint:contextcheck ...` is unused
```

"Your changes" is the dirty working copy, so a run starting against an
uncommitted tree is handed every outstanding failure in it as its own. All
three turns went to the daemon lane's lint, and the run ended reporting the
daemon fix rather than the task it was given. It also fixed the gosec hit
wrongly, leaving `w.Close() //nolint:errcheck` against a G104 that
`errcheck` does not own, so the lint is still open.

Sequenced differently this would not have happened: the first lane ran on a
clean tree and finished its own task in 23 turns. What the shared tree cost was the work
itself, because the second and third lanes never touched what they were
asked.

## Why the local server served nothing, proved

The fourth lane was asked to diagnose it and report without editing Go, and
it did, in 6 turns and $0.07: `_ai_/bench/router-fast-tier.md`. Checked by
hand against `internal/router/router.go`, its reading holds.
`router.Default` is `ChoiceBalanced`, and `Route` moves in two directions
only: fast down to balanced when a pinned-fast turn exceeds
`FastBudget(window)`, and upward through `escalate` on a prior failure.
Nothing promotes a turn to fast, and the doc comment on `Default` says so
("until a task-shape signal exists to choose them"). The overflow picker
chooses an endpoint for a tier already chosen, so machine load cannot pull a
balanced turn onto the local server either.

So the fast tier is pin-only, and `wavezd` starts and holds a llama-server
on every launch for a tier nothing selects on its own: about 30 seconds of
silent startup and several GB resident, for zero turns unless somebody pins
a thread. Starting it on the first fast-tier turn instead is the obvious
shape, and it is DESIGN's open routing item that decides whether there is
ever a first one.

## State

The session landed as four commits on `main`, whose tip moved mid-session
from another session. `go build ./...`, `go test ./...`, and
`golangci-lint run` over the whole module are clean. The one lint left by a
lane was `w.Close() //nolint:errcheck` against a gosec G104 that `errcheck`
does not own, fixed by hand to check the error.

Of the findings above, the announce-before-bind bug, the wrong header, and
the invisible live spend are fixed, each with a test that fails without its
fix. The shared gate and shared tree was taken as a design question and is
answered in DESIGN: the tree stays shared and what a gate reports gets
scoped, because a run's change set is already its own and only the gates'
execution reads the whole working copy. Reading the code moved the
diagnosis: a lease would have prevented neither lane's loss, and neither
would a change to the change set.
