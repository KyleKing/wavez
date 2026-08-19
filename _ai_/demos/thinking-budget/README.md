# thinking-budget

Spike from `_ai_/research/2026-08-efficiency-frontier.md`: did turning a
hybrid model's thinking off cost landed edits, or only tokens? The decision
in DESIGN.md was measured on tokens (79 to 2 on a one-word reply) and wall
time; this measures it on the edit loop, with a token budget as the third
arm llama-server now offers (`--reasoning-budget`).

## Method

`run.sh <gguf> [samples]` runs the four timing-harness tasks
(`_ai_/bench/timing/tasks.txt`: q1 question, e1 one-line edit, e2 method plus
table test, e3 rename across a package) through `wavez -p -model local` in
three arms, each sample a fresh bench tree, and `summarize.py` folds the
rows with each run's own JSON (stop, output tokens, tool calls):

| Arm | How |
|---|---|
| off | wavez starts `llama-server` itself with `enable_thinking=false`, as shipped |
| on | a `llama-server --reasoning on` started first, which wavez finds on its port and reuses |
| budget | the same with `--reasoning-budget 256` |

A landed edit is e1 with the one file changed and exit 0, or e2/e3 with a
change that builds and passes the touched packages' tests. q1 is excluded
from the edit count.

## Numbers

2026-08-19, M4 Pro 24 GB, `qwen3:8b` Q4_K_M, llama-server build 10470,
`contextWindow = 32768`, three samples per task per arm, a model pull
running in the background (network only):

| Arm | landed edits | mean wall (s) | mean output tokens | mean tool calls | stops |
|---|---|---|---|---|---|
| off | 3/9 | 51.5 | 163 (3 runs kept) | 3.7 | complete 2, loop_detected 1 |
| on | 3/9 | 143.6 | 4,111 | 4.6 | complete 6, deadline 3, loop_detected 2, stagnant 1 |
| budget 256 | 3/9 | 86.9 | 2,292 | 6.4 | complete 6, stagnant 3, loop_detected 2, deadline 1 |

The three landed edits in every arm are the three e1 samples; e2 and e3
landed in no arm, on or off. Per-run rows are in `thinking-budget.jsonl`
beside the bench copy when this was run; the first eleven `off` rows have no
kept JSON because the keeper started late, so that arm's token mean is from
three runs.

## What it says

- Thinking did not buy an edit on this model: the same three of nine landed
  with it off, on, and capped, and the two tasks that fail fail the same
  way (a rename that loops, a method plus test that does not build)
- It cost wall time (2.8x on, 1.7x capped) and output tokens (25x on, 14x
  capped), and under `on` three runs hit the 300 s deadline that no `off`
  run reached
- A 256-token budget is per response, not per run: a loop of seven turns
  still spent about 2,300 tokens thinking. The budget halves the damage and
  does not change the outcome, so the switch stays a switch. The spike's
  own bar ("a 200-token budget lands more edits than off at under 2x the
  wall time") is not met on either count
- Not measured: a stronger local model (the `qwen3.8:27b` probe is the
  natural rerun, since thinking may matter more where the base model can
  already do the edit), the hosted tier, and Nemotron-style
  `max_thinking_tokens`
