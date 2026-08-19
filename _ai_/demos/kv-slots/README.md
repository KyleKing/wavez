# kv-slots

Spike from `_ai_/research/2026-08-efficiency-frontier.md`: with `-np 1`, what
does a second thread cost the first? DESIGN.md's fleet decision assumed
threads evict each other's cached prefix on every switch and weighed one
slot per thread (memory) against slot save and restore (disk) to avoid it.

## Method

`run.py <gguf> [turns]` starts a fresh `llama-server` per arm on port 8091
and drives two scripted threads, A and B, each with a distinct ~3.6k-token
system prefix, alternating one short turn each. Every request's `timings`
block gives `prompt_n` (tokens prefilled), `cache_n` (tokens reused), and
`prompt_ms`; the server's resident set is read with `ps` after each arm.

| Arm | Flags | What it models |
|---|---|---|
| np1 | `-np 1 -c 8192` | what wavez ships, with llama-server's host-RAM prompt cache at its default (`--cache-ram 8192` MiB) |
| np1-noram | `-np 1 -c 8192 --cache-ram 0` | the eviction the design assumed |
| np2 | `-np 2 -c 16384` | one 8k slot per thread |
| np1-save | `-np 1 -c 8192 --slot-save-path` | save the idle thread's slot to disk and restore it around each switch |

## Numbers

2026-08-18, M4 Pro 24 GB, `qwen3:8b` Q4_K_M, llama-server build 10470,
three turns per thread, models still downloading in the background (network
only). `prompt_ms` after the first turn is the switch cost, summed over the
two later turns of each thread:

| Arm | first-turn prefill (A, B) | switch cost, A / B | RSS after |
|---|---|---|---|
| np1 | 10.7 s / 10.7 s | 0.26 s / 0.24 s (`cache_n` 3,622 on every switch) | 8.8 GB |
| np1-noram | 10.7 s / 10.7 s | 22.2 s / 22.4 s (`cache_n` 10: full re-prefill each switch) | 6.3 GB |
| np2 | 10.7 s / 10.7 s | 0.25 s / 0.26 s | 10.0 GB |
| np1-save | 10.7 s / 10.7 s | 0.27 s / 0.27 s, plus 35 ms per restore | 6.8 GB |

Prefill ran at about 340 tokens/s and decode at about 40 tokens/s on this
machine, against the M2 Pro's 18-21.5 tokens/s decode in the audit.

## What it says

- The premise is gone: on this llama-server, `-np 1` already keeps an idle
  slot's KV in host RAM ([llama.cpp#16391](https://github.com/ggml-org/llama.cpp/pull/16391))
  and a switch between two ~3.6k prefixes costs a quarter of a second, the
  same as a second slot and the same as a disk restore. Serializing local
  turns under one slot loses nothing to prefill until the cache fills
- What it costs is memory the admission scheduler does not see: the RAM
  cache defaults to 8 GiB and this run grew the server from 6.3 GB to
  8.8 GB with two prefixes cached, so a fleet of threads on the local tier
  can add up to 8 GiB to `llama-server`'s footprint before a single extra
  slot is configured. `--cache-ram` is the knob to size against the
  admission headroom, not `-np`
- `-np 2` bought nothing here (0.25 s versus 0.26 s) and cost 1.2 GB more
  for the second 8k slot; the case for extra slots is concurrency (two
  turns decoding at once), which this alternating harness does not exercise
- Slot save and restore works (35 ms for a 3.6k slot) and is the fallback
  if the RAM cache has to be capped small on the 16 GB laptop

Not measured: more than two threads, prefixes near the window, `-np 2` with
both threads decoding at once, and the same on the M2 Pro's 16 GB.
