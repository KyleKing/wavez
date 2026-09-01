# Why the fast tier served zero turns

Threads `b7700e71ac4c6d10` (23 agent turns) and `af940427c417b9b6` (15 agent
turns) logged `tier: "balanced"`, `model: "glm-5.3"` on every KindAgent event,
while a llama-server for qwen3:8b sat idle on this laptop. The short answer:
**the router has no path that ever promotes a turn to fast.** Fast is reachable
only through an explicit per-thread/per-run override, and these two threads
never had one. The overflow picker cannot change that, because it chooses an
*endpoint* for a tier already chosen, not a tier.

## Evidence

### 1. `router.Route` starts at Balanced and only moves up

`internal/router/router.go`:

```go
// Default is the tier a turn runs on when nothing pins it. Neither
// neighbor is reached automatically: fast and deep are pinned per thread or
// per run until a task-shape signal exists to choose them (DESIGN.md "Model
// routing").
const Default = ChoiceBalanced
```

and in `Route`:

```go
base, reason := Default, "default tier"
if in.Override != "" {
    base, reason = in.Override, "explicit override"
}

if base == ChoiceFast && in.EstimatedTokens > FastBudget(in.Window) {
    base, reason = ChoiceBalanced, "over the fast tier's context budget"
}

if in.PriorFailures > 0 {
    if up := escalate(base, in.PriorFailures); up != base { ... }
}
```

Three observations:

- With an empty `Override`, `base` is `ChoiceBalanced`. Nothing downstream can
  lower it: the window check only *demotes* fast to balanced, and
  `escalate`/`PriorFailures` only move up the `order`
  (`[fast, balanced, deep]`). Fast is a pin-only tier by design — the doc
  comment says so outright ("until a task-shape signal exists to choose them").
  No such signal has been implemented.

- Even the demotion path is dead unless the caller pins fast: the
  `in.EstimatedTokens > FastBudget(in.Window)` check is guarded by
  `base == ChoiceFast`.

### 2. The daemon hands the loop an empty override for these threads

`internal/daemon/manager.go:544-575`, `runTurn`:

```go
override, thinking := mt.override, mt.thinking
...
route := router.Input{Override: override, Thinking: thinking}
...
outcome, err := m.loop.Run(runCtx, mt.th, m.prefix, expanded, route)
```

`mt.override` is whatever the client set with the daemon's `route` command
(`setOverride`, manager.go:423). The thread logs show no route events, and the
first turn of `b7700e71ac4c6d10` was served balanced within ~5s of the user
prompt — consistent with `override == ""` for the whole run. The CLI's
`routerHint` (cmd/wavez/main.go:531) is likewise empty unless the user passes
`--model fast`.

### 3. The agent loop passes only override / window / size / failures

`internal/agent/loop.go:810-817`:

```go
func (r *run) routeInput(estimated int) router.Input {
    return router.Input{
        Override:        r.hint.Override,
        Window:          r.loop.ContextWindow(),
        EstimatedTokens: estimated,
        PriorFailures:   r.escalations,
    }
}
```

The only task-shape signal in `Input` that could relate to fast is
`EstimatedTokens` (plus `Window`), and — per §1 — it is used exclusively to
*move away from* fast when pinned there. There is no cheap-task heuristic, no
tool-shape signal, nothing that selects fast. Both threads' first turns were
~3.5k input tokens, comfortably under `FastBudget(8192) = 8192 - 1024 = 7168`,
so even a hypothetical fit check would have passed; the check simply runs in
the wrong direction and only for pinned-fast turns.

The same is true of the other Route caller: the reviewer pins Balanced
explicitly (`internal/app/reviewer.go:127`:
`router.Input{Override: router.ChoiceBalanced, ...}`).

### 4. The overflow picker picks an endpoint, not a tier

`internal/llm/overflow/overflow.go:47-53`:

```go
func (p *Provider) pick(ctx context.Context) llm.Provider {
    if p.elsewhen == nil || p.busy == nil || !p.busy(ctx) {
        return p.local
    }
    return p.elsewhen
}
```

`overflow.New` wraps exactly one tier's provider (`internal/app/app.go:680-689`,
`fastProvider`): it decides whether the *fast* tier's turn is served by the
local llama-server or its overflow endpoint. It is consulted inside
`Provider.Stream`, i.e. after `router.Route` has already chosen the tier, so it
can never route a balanced turn to the local qwen3:8b. Machine load (via
`loadedAbove`, app.go:695) only influences which endpoint a fast-tier turn
uses. Notably it is also conservative in the direction that matters here: an
unreadable load reports *busy* (`sysinfo.ReadLoad` error → `true`), but since
no turn ever reached the fast provider, this never came into play either.

## Conclusion

The local llama-server served zero turns because nothing in the system can
choose `ChoiceFast` on its own:

1. `router.Default` is `ChoiceBalanced`, and `Route`'s only movements are
   fast→balanced (context overflow, pinned fast only) and upward escalation on
   failure. The design comment admits the promotion signal "until a task-shape
   signal exists to choose them" — it does not exist.
2. The daemon and CLI pass `router.Input{Override: mt.override}` where the
   override is empty unless the user explicitly pins fast (TUI route cycle,
   palette, `--model fast`, or the daemon `route` command). Neither thread had
   one.
3. The overflow picker is a tier-internal endpoint selector
   (`local` vs `elsewhen` behind one tier), consulted after the tier is chosen,
   so machine load cannot pull a balanced turn down to the fast tier.

To get the local model serving, a user must pin the thread to fast (TUI
`cycleRoute`/palette, `--model fast`, or the daemon's route command) — and even
then, a pinned-fast turn whose estimated tokens exceed
`FastBudget(window) = window - 1024` is silently demoted back to balanced.
