# pkl routine config: is it fast enough to reload on file change

Demo backing the `.wavez.pkl` design question: can pkl-go evaluate a routine config fast
enough to reload on every file-change event, and can hk's `hk.pkl` import it.

## Numbers

Go benchmark (`main.go`), evaluator reused across 20 warm iterations, `/usr/bin/time -l`
for RSS:

| metric                          | value                    |
| -------------------------------- | ------------------------ |
| cold eval, first process ever    | 166 ms (one-time, OS cache cold) |
| cold eval, steady state          | 10-14 ms (fresh process each run) |
| warm eval, avg (20 iters)        | ~130 µs |
| warm eval, median                | ~125 µs |
| warm eval, min/max               | ~105 µs / ~210 µs |
| maximum resident set size        | ~30 MB (Go binary + spawned `pkl server` child) |
| peak memory footprint (`time -l`)| ~6.3 MB |

CLI comparison (`pkl eval .wavez.pkl -f json`, separate process per call, no server
reuse): ~10-50 ms per invocation, dominated by process startup since each call is a
fresh evaluator with nothing to reuse.

pkl-go spawns a real `pkl server` subprocess (`pkl/evaluator_manager_exec_unix.go`,
`exec.Command(exe, "server")`) and talks to it over a persistent msgpack RPC channel.
The `NewEvaluator` call pays that spawn cost once; every `EvaluateModule` after that
reuses the same server process, which is why warm calls land in the hundreds of
microseconds rather than milliseconds.

The warm path is comfortably under the 50 ms target: it's roughly 400x faster than that
bar. A file-watcher that keeps one evaluator alive and calls `EvaluateModule` per change
would not be perceptibly slower than reading a JSON file.

## hk import verdict

`hk.pkl` amends `package://.../hk@1.54.0#/Config.pkl` and does
`import ".wavez.pkl" as wavez`, then reads `wavez.routines["fmt"].steps[0].cmd` to build
a step's `fix` command. `pkl eval hk.pkl` on its own evaluates cleanly and threads the
value through (`fix = "gofmt -l -w ."`).

`hk validate` and `hk check --plan` fail against this file under the **default** backend
(`pklr`, hk's Rust-native pkl reimplementation) with `Eval error: key not found: 0`,
a list-indexing bug when reading a `Listing` that came in through an imported module.
Setting `HK_PKL_BACKEND=pkl` (the real pkl binary, same one used above) makes
`hk validate` pass: `hk .../hk.pkl is valid`. `HK_PKL_BACKEND=pklr` reproduces the same
failure as the default, confirming pklr is the backend at fault, not the config.

## Recommendation

1. Use pkl-go with a single long-lived `pkl.Evaluator`, opened once at process start and
   reused for every reload; don't spin up a new evaluator per file-change event.
2. Skip an additional content-hash cache layer: at ~130 µs a warm eval is already far
   cheaper than the file I/O and fs-watch debounce around it, so caching would add
   complexity without a measurable win.
3. For `hk.pkl` importing `.wavez.pkl`, pin `HK_PKL_BACKEND=pkl` (or document it as a
   required env var) until jdx/hk fixes the pklr indexing bug; don't rely on the default.

## Rerun

```sh
cd _ai_/demos/pkl-routines
pkl eval .wavez.pkl -f json                  # schema sanity check
go build -o wavez-bench . && /usr/bin/time -l ./wavez-bench   # cold/warm/RSS numbers
for i in 1 2 3; do /usr/bin/time -p pkl eval .wavez.pkl -f json >/dev/null; done  # CLI timing
pkl eval hk.pkl >/dev/null                   # hk.pkl + wavez import, pkl itself
HK_PKL_BACKEND=pkl hk validate               # passes
HK_PKL_BACKEND=pklr hk validate              # fails: key not found: 0 (default backend bug)
```
