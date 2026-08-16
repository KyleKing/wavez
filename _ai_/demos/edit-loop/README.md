# edit-loop: str_replace vs hashline for qwen3:8b

Spike comparing two edit-tool formats inside a real agentic tool-use loop against
`qwen3:8b` via Ollama, on 5 small Go edits x 2 formats x 2 runs = 20 runs.

- `str_replace`: exact-match old_string/new_string, Anthropic-style.
- `hashline`: `read_file` renders each line as `N#hh| text` (hh = 2-char content
  hash); `edit_file` takes ops (`replace`/`insert_after`/`delete`) anchored by
  `N#hh`, and any op whose hash doesn't match the current file rejects the
  whole call before applying anything.

Both formats shared the same loop, model, tasks, and `run` tool (`go build
./... && go vet ./...`, the only allowed command). Max 12 turns, stop on an
identical repeated tool call. `think: false`.

## Results

Success below is my own read of whether the task spec was met, not just
whether `run` passed. All 4 T3 runs (both formats) passed build+vet but never
touched `predicate_test.go`, so they don't count as successes even though the
harness's automated flag said so (build+vet can't see a spec violation that
doesn't break compilation).

| format | success (of 10) | malformed calls (total / avg) | avg turns | avg output tokens | avg wall time | stale-hash / no-match failures |
|---|---|---|---|---|---|---|
| str_replace | 2 | 12 / 1.2 | 4.2 | 190 | 12.5s | 12 |
| hashline | 1 | 11 / 1.1 | 7.3 | 605 | 37.7s | 11 |

Per-task pass/fail (my read, both runs):

| task | str_replace | hashline |
|---|---|---|
| T1 rename variable | 0/2 | 0/2 |
| T2 guard clause | 2/2 | 0/2 |
| T3 format string + test | 0/2 (test never edited) | 0/2 (test never edited) |
| T4 switch case | 0/2 | 1/2 |
| T5 extract expression | 0/2 | 0/2 |

## Notable failure modes

**str_replace: hallucinated indentation.** T1 (rename `aCount`/`bCount`), the
model's `old_string` used 4 tabs where the file has 1:
`'aCount := a.UncommittedCount()\n\t\t\t\tbCount := b.UncommittedCount()'`.
Rejected as "not found", repeated verbatim on retry, loop stopped on repeat
detection. It read the file first turn and still got the whitespace wrong from
memory rather than copying it.

**str_replace: self-defeating non-uniqueness.** T5 asks the model to extract a
duplicated expression into a local. Its `old_string` was exactly the
duplicated expression, so it matched twice and got rejected
("matches 2 times, must match exactly once"). The task's own premise (the
expression appears twice) breaks str_replace's uniqueness requirement, and the
model never adapted its `old_string` to disambiguate.

**hashline: never produced a well-formed anchor under pressure.** Every single
malformed hashline call across all 20 runs (11 of them) failed on anchor
*format*, not a genuine stale value: anchors like `'24#a8| func homogeneousKind...'`
(copied the whole rendered line, including the `| text` part, instead of just
`N#hh`) or `'49#66|'` (dropped/mangled the 2-char hash). The model understood
"line number plus something" but not the exact 2-character contract, even
though `read_file`'s output showed the format on every line.

**hashline: silent stalls.** In 3 of 10 hashline runs (T4 run2, T5 run1, T5
run2) the model called `read_file` once, then returned empty content and no
tool_calls for the remaining 11 turns straight through to the turn cap. The
"call a tool" nudge message didn't recover it. This didn't happen at all with
str_replace, where every stall was a repeated malformed call, not silence.

## Verdict

Ship `str_replace` for v0.1. It succeeded twice as often, used a third of the
output tokens, and finished in a third of the wall time, because the model
already has heavy str_replace/diff exposure from training and needs no
explanation of a bespoke anchor scheme. Neither format is good yet (2/10 and
1/10 by strict spec-reading), so v0.1 should keep edits small and always
re-verify with `run` rather than trusting `done`.

Hashline's hash-rejection never got to prove itself: every hashline failure in
this run was the model failing to produce a syntactically valid `N#hh` anchor,
not the model using a stale-but-well-formed hash after the file moved under
it. So no, the rejection mechanism didn't demonstrably save a wrong edit here
because it never reached a scenario where a wrong-but-well-formed edit was on
the table. Its cost (more output tokens explaining anchors, more turns) is
proven; its safety benefit is not, at least not against this model at this
task size.

## Rerun

```
cd /Users/kyleking/Developer/kyleking/wavez/_ai_/demos/edit-loop
ollama serve &
./run_all.sh
pkill -f 'ollama serve'
```

Single run: `go run . -format str_replace -task T1 -run 1 -root .`

Raw per-turn logs: `logs/<format>-<task>-run<N>.jsonl`. Aggregate: `logs/results.jsonl`.
