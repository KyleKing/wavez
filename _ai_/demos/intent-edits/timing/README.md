# Intent-edit timing bar

Measures wall time, tokens, and correctness for three realistic Go edit
tasks in three modes, on copies of `gh-repo-dashboard` files. Goal: find
the bar the intent-edit tool must clear ("faster than a hosted frontier
model at editing and equivalent code").

Environment: macOS, Ollama 0.32.13 (already running, not started or
stopped by this run), `qwen3:8b` (5.2GB, already pulled), `claude` CLI
(Claude Code), `claude-sonnet-5` and one `claude-haiku-4-5` run.

## Tasks

- **T1** (`tasks/T1.md`): add `HostFromRemote` to `vcs/identity.go`, a
  ~15-line sibling of `RemoteIdentity`.
- **T2** (`tasks/T2.md`): add `FilterMode.Next()` to `models/enums.go` plus
  a table-driven test row, mirroring `SortMode.Next()`.
- **T3** (`tasks/T3.md`): add `SizeBytes int64` to `NoteFile`, thread it
  through `DetectNotes` and one caller (`cli.newRepo`), update the test
  table.

Each spec embeds the full current file content, so every mode gets the
same information. Baselines live in `copies/{t1,t2,t3}/`, minimal
standalone Go modules.

## Modes

- **A hosted-full**: `claude -p <task> --model claude-sonnet-5
  --output-format json` (plus 1 run at `claude-haiku-4-5`), asked for the
  complete file(s). 2 runs/task, Sonnet; 1 run, Haiku.
- **B local-full**: same prompt to `qwen3:8b` via Ollama `/api/chat`,
  `think: false`, non-streaming. 2 runs/task.
- **C local-intent+hole**: simulated pipeline. (1) `qwen3:8b` emits one
  intent line (`add fn NAME(PARAMS) RESULTS in PKG near SYMBOL`). (2) a
  skeleton is hand-built (assumed deterministic resolver, ~50ms, not
  measured). (3) `qwen3:8b` fills only the body given prefix/suffix. 2
  runs/task, both phases.

Mode C targets the function-shaped core of each task, not the secondary
test-file edits, since the intent grammar is fn-centric rather than
struct-field- or test-table-centric. For T3, the hole-fill step assumes
`SizeBytes` already exists (a prior intent in a real pipeline); the
verifier patches it in before compiling.

FIM check: `POST /api/generate` with a `suffix` field against `qwen3:8b`
returns `"does not support insert"`, confirming no native FIM support, so
body-fill uses a plain chat prompt with prefix/suffix as context, not true
infill. A FIM-tuned model (`qwen2.5-coder`) would be needed for that; not
pulled, per instructions.

## Results

Wall time is full round-trip (process start to response). Tokens are
`prompt_eval_count`/`eval_count` (Ollama) or the summed
input/cache-read/cache-creation and output tokens (Claude). Compiles means
`go build && go vet` passed on the verifier copy. Correct means the output
matches the spec by reading (tests pass where the task added them; for T1,
hand-checked against `RemoteIdentity`'s parsing).

| Task | Mode | Wall (avg, 2 runs) | In tok | Out tok | Cost | Compiles | Correct |
|---|---|---|---|---|---|---|---|
| T1 | A hosted (Sonnet) | 17.1s | 18,979 | ~2,220 | $0.055 | 2/2 | 2/2 |
| T1 | B local-full | 74.5s | 1,461 | ~1,365 | n/a | 2/2 | 2/2 |
| T1 | C intent+hole | 9.9s | ~268 | ~109 avg | n/a | 2/2 | **0/2** |
| T2 | A hosted (Sonnet) | 26.0s | 20,825 | ~4,137 | $0.091 | 2/2 | 2/2 |
| T2 | A hosted (Haiku) | 29.4s | 16,114 | 3,804 | $0.037 | 1/1 | 1/1 |
| T2 | B local-full | 169.6s | 2,553 | ~2,213 | n/a | **0/2** | 0/2 |
| T2 | C intent+hole | 4.9s | ~227 | ~17 avg | n/a | **0/2** | 0/2 |
| T3 | A hosted (Sonnet) | 34.3s | 20,297 | ~4,797 | $0.099 | 2/2 | 2/2 |
| T3 | B local-full | 239.3s | 2,427 | ~1,938 | n/a | 2/2 | **0/2** |
| T3 | C intent+hole | 5.4s | ~220 | ~26 avg | n/a | 2/2 | 2/2* |

\* T3 intent+hole correctness assumes the `SizeBytes` field intent already
landed (see Mode C note above), so it isn't re-deriving the full task.

Ratios (mean wall time across all 6 runs per mode, tasks pooled):

- hosted (Sonnet): 25.8s
- local-full: 161.1s, **6.2x slower** than hosted
- intent+hole: 6.7s, **3.9x faster** than hosted, **24x faster** than local-full

## Verdict

Intent+hole clears the speed bar decisively: 6.7s average against
Sonnet's 25.8s, since it generates only an 8 to 20 token intent line plus
a few-dozen-token body, never a full file. It does not clear the
equivalent-code bar here: 2 of 3 body-fills were wrong or invalid (T1
missed the SCP-URL host case, T2 called `len()` on a non-collection
type). Local-full on the same small model was worse on both axes, 6.2x
slower than hosted and only 2 of 6 tasks correct, so pointing `qwen3:8b`
at the whole file buys nothing.

The gap is qwen3:8b's reasoning at small output budgets, not the
architecture. T3's hole-fill (a 3-line sum loop) was correct both runs;
T1 and T2 needed more judgment (URL edge cases, a valid cyclic-enum
idiom) than an 8B model reliably supplies without a verify-and-retry
loop. Batching multiple intents into one turn would not fix this: the
failures were reasoning failures inside an already-small generation, not
round-trip overhead. Batching would mainly shave the per-call 120 to 140
input-token tax visible in `prompt_eval_count` above, worth maybe 20 to
30% off Mode C's wall time, not a correctness fix.

## Rerunning

```bash
cd _ai_/demos/intent-edits/timing

# Mode A (hosted), writes logs/hosted_<task>_<model>_run<n>.json
./run_hosted.sh T1 1 claude-sonnet-5

# Mode B (local-full), writes logs/local_full_<task>_run<n>.json
python3 run_local_full.py T1 1

# Mode C phase 1 (intent line), writes logs/intent_line_<task>_run<n>.json
python3 run_intent_line.py T1 1
# Mode C phase 2 (body fill), writes logs/body_fill_<task>_run<n>.json,
# logs/results/body_fill_<task>_run<n>.go (assembled file)
python3 run_body_fill.py T1 1

# Verification
python3 verify.py T1 logs/results/hosted_T1_claude-sonnet-5_run1.txt run1  # modes A/B
python3 verify_hole.py T1 logs/results/body_fill_T1_run1.go run1          # mode C
```

`copies/{t1,t2,t3}/` are the standalone baseline modules; `verify.py` and
`verify_hole.py` each copy one into `logs/verify/` per run and `go
build`/`go vet`/`go test` it. Raw model responses are in `logs/`; assembled
source files ready to compile are in `logs/results/`.
