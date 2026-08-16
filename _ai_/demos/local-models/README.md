# Local model bench: gemma4:12b vs qwen3:8b

Machine: MacBook Pro M2 Pro, 16 GB unified memory, macOS 26.6.1. Ollama 0.32.13
installed via `brew install ollama` (was not present before this run).

`qwen3-coder:30b-a3b` was skipped: the smallest quant listed on ollama.com is
`q4_K_M` at 19 GB, over the ~12 GB budget set for that model.

Scripts: `scripts/setup.sh` (install/pull), `scripts/bench.py` (all measurements,
stdlib only, re-run with `python3 scripts/bench.py <model> logs`),
`scripts/coding_prompt.txt` (the ~450-token coding prompt used for the decode test).
Raw JSON and server logs are in `logs/`.

## Results

| Metric | qwen3:8b | gemma4:12b |
|---|---|---|
| Disk size | 5.2 GB | 7.6 GB |
| Resident (ollama ps) | 5.9 GB | 8.0 GB |
| Cold load, wall time | 13.5 s | 14.2 s |
| Free memory before load | 69% | 82% |
| Free memory after load | 31% | 30% |
| Decode speed (3 runs, avg) | 18.3 tok/s | 13.7 tok/s |
| Prompt eval speed, cold (run 1) | 176 tok/s | 134 tok/s |
| Prompt eval speed, warm (runs 2-3, same prompt) | 11,600-15,200 tok/s | 3,185-3,262 tok/s |
| Tool call well-formed | 3/3 | 2/3 |
| Tool call correctness | 3/3 called `rename_symbol` | 2/3 called `edit_file`, 1/3 hallucinated a tool (`list_files_recursive`) not in the tool list |
| 2k-token prefix cache speedup | 201x (9.44s to 46.8ms) | 43x (16.2s to 380ms) |
| Default served context (`ollama ps`) | 4096 | 4096 |
| Max trained context (`ollama show`) | 40,960 | 262,144 |

Decode runs for gemma4:12b were capped at `num_predict: 300`. An uncapped first
attempt hit gemma4's thinking mode, which produced a 2,600+ token response and,
combined with memory pressure, dropped decode speed from ~13 tok/s to ~2 tok/s
mid-generation before finishing. That attempt is recorded in
`logs/gemma4_12b-run.log` (overwritten) and is why the reported decode number
uses the capped rerun. qwen3:8b's responses self-terminated at 547-780 tokens
without a cap.

Both models' own decode-test prompt eval numbers rise sharply on repeat runs
because they reuse the same coding prompt each time, which the server caches
automatically, same effect as the dedicated prefix-cache test but on a shorter
(~700-token) prefix.

Memory pressure while gemma4:12b was decoding dropped to 14-18% free
(`memory_pressure -Q`), worse than the 30% seen right after load, and the
uncapped run showed visible thrashing (decode speed collapsing to ~2 tok/s).
qwen3:8b did not show this pattern in any run.

## Conclusion

Pick qwen3:8b for edits with tool calls: faster decode, 3/3 well-formed and
correctly-named tool calls versus 2/3 for gemma4:12b, and one outright
hallucinated tool name from gemma4:12b.

Pick qwen3:8b for compaction too, on this hardware. gemma4:12b's larger
trained context (262k vs 41k) matters less than the fact that raising context
past the 4096 default multiplies KV cache memory, and gemma4:12b already
thrashes under memory pressure at the default context and short outputs.

The 16 GB machine has thin headroom for a Go test suite (~1-2 GB) while
qwen3:8b is loaded (31% free after load, ~5 GB), but not while gemma4:12b is
loaded and generating (free memory fell to 14-18%). Run test suites before
loading gemma4:12b or after unloading it, not concurrently.
