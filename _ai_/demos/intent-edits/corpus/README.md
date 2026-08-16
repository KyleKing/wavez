# Intent edits: twenty-commit demo

Measures how much of a real commit an intent-edit resolver (see [`DESIGN.md`](../../../../DESIGN.md#intent-edits)) could reproduce without a model, against how much needs a small model's hole fill or a human/model design call.

## Method

Twenty commits: 14 from `gh-repo-dashboard` (Go, Bubble Tea), 6 from `calcipy` (Python). Picked feature and fix commits between 20 and 400 changed lines, skipped version bumps, dependency bumps, and pure-docs commits. For each commit I read the full diff (`git show`) and, for the added lines only, sorted them by hand into four buckets:

- **deterministic**: imports, signatures, struct fields, boilerplate, registration, test scaffolding, formatting, doc lines that mirror a sibling, mechanical caller updates
- **convention**: gettable right from sibling code in the same package (error-wrapping style, log lines, naming, fixture use)
- **hole**: logic a small model has to write (a condition, an algorithm, a message, UI layout)
- **judgment**: a design decision no resolver could make

Then wrote 1-3 intent lines per commit in the design's grammar, inventing extensions where the grammar had no verb for what the commit did, and estimated intent tokens as word count times 1.3. Per-commit detail is in `intents.md`; raw counts are in `corpus.csv`.

Bucket assignment is subjective, done by one person reading diffs once. Boundaries between "convention" and "hole" especially move depending on how generous you are about what a code-graph store could infer. Treat the percentages as an order-of-magnitude read, not a benchmark score.

## Aggregate

Lines are added lines only (2,468 total: 1,607 gh-repo-dashboard, 861 calcipy).

| | deterministic | convention | hole | judgment |
|---|---|---|---|---|
| gh-repo-dashboard | 593 (36.9%) | 324 (20.2%) | 484 (30.1%) | 206 (12.8%) |
| calcipy | 177 (20.6%) | 274 (31.8%) | 119 (13.8%) | 291 (33.8%) |
| overall | 770 (31.2%) | 598 (24.2%) | 603 (24.4%) | 497 (20.1%) |

Resolver-only (deterministic + convention) covers 55.4% of added lines overall: 57.1% in gh-repo-dashboard, 52.4% in calcipy. Add the hole (a small model fills 5-30 tokens per hole, not a function) and the two layers together account for 79.9% of added lines. The remaining fifth is judgment: no resolver, no small model, a real decision.

calcipy's judgment share is inflated by one commit, `f55353fc` (the vale/ADR commit), where 251 of 351 added lines are prose in two Markdown documents, a decision write-up no resolver or hole-fill model produces. Drop that commit and calcipy's judgment share falls from 33.8% to 7.8%, deterministic+convention rises to 76.7%, close to gh-repo-dashboard's number. One documentation-heavy commit swings the repo average by 26 points, which says more about corpus size than about calcipy.

## Intent tokens vs. lines produced

Total intent tokens across the corpus: 1,366 (1,051 gh-repo-dashboard, 315 calcipy). Total added lines: 2,468. Average per commit: 68 intent tokens for 123 added lines, a ratio of about 1.8 lines of diff per intent token.

Per-commit tokens ranged from 42 (`ef745676`, a dependency bump plus two mirrored tests) to 118 (`afdfbbe`, eight distinct additions needing eight intent lines). Commits needing one `like` line stayed under 50 tokens; commits needing four or more separate adds (new field, new fn, new wiring, new test) ran 90-120 tokens because each addition needs its own intent line, not because any single line got longer.

## Top 5 grammar extensions the corpus demanded

1. A `change`/`rename` verb for editing an existing symbol's shape (signature change, enum collapse, module rename), not just adding a new one. Needed in 6 of 20 commits (`a01befc`, `a5bbb28`, `b63beb01`, `d069317`, `5a22239`, and implicitly `cd69133`). The base grammar only has "add."
2. A `like`-chain that mirrors a cluster of symbols together (a cache var, its key builder, its cached-read wrapper) rather than one symbol at a time. Needed in `44588fd`, `cd69133`, `afdfbbe`.
3. Hole-sizing annotation fields on an `add fn` line: `wraps=`, `cmd=`, `env=`, `cap=`, `handles=`, `returns=`, `group-by=`, `enum=`. These don't fill the hole, they bound it, telling the small model what shape of logic to write. Needed in 5 commits.
4. A `fix: <diagnosis>` verb for bug fixes that add no new symbol, only correct an existing one's behavior (`884fbcf`). The diagnosis itself, not the code change, is the content; there's no "add" to anchor an intent line to.
5. Non-code artifact verbs: `add doc <path>` for prose authoring (`f55353fc`), `add const`/`add hook` for bare constants and pre-commit registrations (`a72a9cd6`, `5249714`), `bump dep` for a dependency-driven change (`ef745676`).

## Best and worst cases

Ranked by resolver-only coverage (deterministic + convention) of added lines.

Best: `ef745676` (100%, dependency bump plus two `like`-mirrored tests, nothing to invent), `44588fd` (93%, a full cache-persistence pattern mirrored from a same-file sibling), `a5bbb28` (88%, an enum collapse that's a mechanical rename plus a doc paragraph).

Worst: `f55353fc` (17%, mostly an ADR and a research doc, a writing task not a code task), `c497fd6` (24%, a three-branch cascading-dismiss priority order that's a genuine UX call), `df88120` (30%, a caption-and-padding UI decision with no sibling to copy).

The pattern: the resolver wins when the commit's core content is "do this again, elsewhere" (a sibling exists) and loses when the commit's core content is a decision (an order, a threshold, a paragraph of rationale) that only exists because someone thought about it.

## Verdict

"10-30 tokens of intent for 50-200 lines" holds for a single `like` line against a single mirrored addition, several commits here land close to that (`ef745676`: 42 tokens for 68 lines; `a5bbb28`: 62 tokens for 32 lines). It does not hold at the commit level once a change touches four or five things (a field, a function, a call site, a test), because each addition needs its own intent line and the tokens add up linearly, not the lines. The corpus average, 68 tokens for 123 lines, is still a large win over emitting 123 lines of text, just not the 4-10x compression the single-line example implies.

The more load-bearing number is the 55% resolver-only floor: over half of what a commit changes needs no model at all if the store and tooling exist to do placement, wiring, and mirroring. The 24% hole share is the part a small local model is actually needed for. The 20% judgment share is the part no version of this system removes; it just makes that 20% cheaper to reach because the other 80% stopped costing tokens.
