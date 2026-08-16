# Local runtime spike: Ollama vs llama-server

Hardware: Apple M2 Pro, 16 GB unified memory, macOS 26.6.1.
Ollama 0.32.13 (bundles its own llama-server internally, confirmed via `ps`).
llama.cpp installed via `brew install llama.cpp`, build 10450 (commit ece963f41).
Model: `qwen3:8b`, same GGUF for both runtimes: the Ollama blob at
`~/.ollama/models/blobs/sha256-a3de86cd1c132c822487ededd47a324c50491393e6565cd14bafa40d0b8e686f`
(found via `ollama show qwen3:8b --modelfile`). Only one server ran at a time; running both
against the same 6.4 GB model OOM'd Metal (`kIOGPUCommandBufferCallbackErrorOutOfMemory`,
see `logs/llamacpp_decode_server.log`), confirming the constraint is real on 16 GB, not just
a rule I followed.

All raw JSON is in `logs/`. Scripts: `bench_ollama_load.py`, `bench_llamacpp_load.py`,
`bench_decode.py`, `bench_prefix.py`, `bench_edit.py`, `bench_tools.py`.

## 1. Cold-start load (3 runs each, ctx 8192)

| | wall to first token | server RSS |
|---|---|---|
| Ollama (`ollama stop` between runs) | 1.65-1.97s | ~6.24 GB |
| llama-server (fresh process each run) | 1.60-1.62s | ~6.24 GB |

Command:
```
ollama stop qwen3:8b && curl .../api/chat -d '{"model":"qwen3:8b","stream":true,"think":false,"options":{"num_ctx":8192}, ...}'
llama-server -m $GGUF -c 8192 -np 1 --port 8090 --reasoning-budget 0
```
Nearly identical numbers, expected: Ollama's runner *is* llama-server underneath
(`/opt/homebrew/Cellar/ollama/.../llama-server --no-jinja --chat-template chatml ...`).

## 2. Decode / prompt-eval on ~380-token coding prompt, think off (3 runs)

| | prompt-eval tok/s (cold) | decode tok/s |
|---|---|---|
| Ollama | 220 (cold), 9000+ once cached | 20.2-21.2 |
| llama-server | 263 (cold) | 19.8-20.9 |

Think-off: Ollama via `"think": false`; llama-server via `"reasoning_effort": "none"` in
the request body (`--reasoning-budget 0` server flag also works, tested and equivalent).
Decode speed is identical within noise, as expected for the same weights on the same
hardware. Both runtimes cache the identical repeated prompt, which is why prompt-eval
jumps 40x on later runs, foreshadowing part 3.

## 3. Prefix cache: 3k-token stable prefix + 3 short suffixes

llama-server run with `--cache-reuse 256`. Ollama has no equivalent flag; caching is
on by default and not configurable.

| step | llama-server prompt-eval | Ollama prompt-eval |
|---|---|---|
| prefix+suffix1 (cold) | 22.1s, 4262 tok, cache_n=0 | 22.3s, 4266 tok |
| prefix+suffix2 | 0.24s, cache_n=4230 | 0.20s (reports full 4260 tok, but duration shows the cache hit) |
| prefix+suffix3 | 0.24s, cache_n=4230 | 0.21s |

Mutating variant: same prefix, one line deleted from the middle (`[tool_result #3]`
removed, simulating a compaction step editing the middle of the context) vs. an
unmodified-prefix control run immediately after:

| variant | llama-server | Ollama |
|---|---|---|
| trimmed middle | 1.16s, cache_n=4014 (178 new tok) | 1.08s, reports 4196/4230 tok re-evaluated |
| unmodified prefix (control) | 1.48s, cache_n=4014 | 1.45s |

Caveat: the control ran immediately after the trimmed request, so both share the single
slot's post-trim cache state rather than isolating a truly clean unmodified-prefix
baseline; treat the two rows as roughly equal, not as proof trimming is free. The clear
signal that holds regardless: an append-only suffix change costs ~0.2-0.25s of
prompt-eval, a mid-document edit costs ~1.1-1.5s, a 5-7x penalty for editing already-cached
context instead of appending to it, on both runtimes.

## 4. Edit-shaped output: 60-line Go file, one function changed, full file back (3 runs)

| | decode tok/s | wall |
|---|---|---|
| Ollama | 20.1 | 26-29s |
| llama-server, no speculation | 19.8-20.0 | 26-28s |
| llama-server, `--spec-type ngram-simple` | 85-88 | 5.9-8.8s |

`llama-server --help` in this build (10450) uses `--spec-type` with values
`ngram-simple`/`ngram-map-k`/`ngram-map-k4v`/`ngram-mod`/`draft-*`, not the older
`--draft`/`--spec-ngram-size` flags (those are removed and error out with a pointer to
the new names). Server log reported draft acceptance 0.879 (464/528 accepted), mean
accepted run length 43 tokens. n-gram speculation is a **4.3x** decode speedup on this
copy-heavy edit prompt, for free (no draft model, no extra memory).

## 5. Tool calling, llama-server `--jinja`, Qwen3 template

One `rename_symbol` call, 3 tries: **3/3 well-formed**, correct name and both args, valid
JSON, no extra text in `content`. Command: `llama-server ... --jinja` (jinja is
enabled by default in this build; `--jinja` makes that explicit), tools passed via
OpenAI-style `tools` array in `/v1/chat/completions`.

## 6. Feature asymmetry

Ollama only: model pull/list/rm/show, automatic model swap and `keep_alive` TTL memory
management, no GGUF path bookkeeping.

llama-server only (exercised here): `--spec-type` n-gram speculation, `--cache-reuse` for
tunable prefix reuse, `--jinja` explicit template control, `--grammar-file` (GBNF, listed
in `--help`, not exercised), and `response_format.json_schema` constrained output,
confirmed working: a request constrained to `{"intent": enum[...]}` returned
`{"intent": "other"}`, valid JSON, no extra text (`logs/json_schema_test.json`). Ollama
also accepts a `format` JSON-schema field on `/api/chat`, not benchmarked here since
llama-server's grammar/spec tooling is the differentiator that matters for v0.1.

## Verdict

v0.1 should talk to **llama-server's OpenAI-compatible endpoint** by default, not Ollama.
Decode and load numbers are a wash (same engine underneath), so the choice comes down to
control: `--spec-type ngram-simple` and `--cache-reuse` aren't available through Ollama's
API at all, and both matter for this agent's workload.

N-gram speculation is worth it: 4.3x decode speedup on copy-heavy edit output for zero
extra memory and one flag, no draft model to manage.

Prompt-prefix reuse justifies an append-only compaction rule: appending costs ~0.2-0.25s
of prompt-eval regardless of prefix size, while editing the middle of an already-cached
prefix costs 5-7x more because the cache can't fully reuse past the edit point. Compaction
should append a summary rather than mutate history in place.
