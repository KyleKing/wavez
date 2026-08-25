# Replay records

`records.jsonl` is the live corpus: runs whose setup is comparable to the one
in `.wavez.pkl` now. Every rate in DESIGN.md comes from it via
`wavez -stats`, so a record taken under different fundamentals does not
belong here, however interesting it was.

`records-archive.jsonl` holds 238 runs from 2026-08-21 through 2026-08-25
that are no longer comparable, kept because they are the only evidence for
decisions already made. Nothing reads them. Three fundamentals had moved by
the time they were retired:

- The deep tier was the same model as the balanced tier, so every escalation
  re-ran the failure on the model that had just produced it
- The tool set and preamble were larger by one tool and 102 tokens
- Parallel lanes shared one jj store with no lock, so a four-lane window
  rewrote its own workspaces mid-run and graded checks against what
  survived. Every multi-lane record before 2026-08-25T15:00Z understates the
  tool by an amount nothing in the record says

Retire a batch by moving it here rather than deleting it, and say in this
file which fundamental moved.
