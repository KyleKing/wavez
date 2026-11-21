# Indexing a large codebase

The code index was built and measured on this repository, which is 604 files
and 5 MB. This is what it takes to hold a checkout two orders of magnitude
past that, and what it costs.

## The corpus

Numbers here come from [getsentry/sentry](https://github.com/getsentry/sentry)
at depth 1: **17,597 source files, 114 MB**, Python and TypeScript/TSX in
roughly equal parts. It is the reference because it is public, it is the
same language mix as the largest tree this project is aimed at, and it is
larger than that tree on both axes, so a design that holds here has margin.

```sh
git clone --depth 1 --filter=blob:none https://github.com/getsentry/sentry ~/src/sentry
WAVEZ_SCALE_ROOT=~/src/sentry go test ./internal/codeintel -run TestScale -v
```

`TestScale` skips without that variable, so it costs nothing in CI and is
the way to re-measure anything below.

## What it costs now

| | this repo | the 114 MB corpus |
|---|---|---|
| first index | 2.2s | 34.9s |
| re-index, nothing changed | 62ms | 239ms |
| `search`, symbol query | 2ms | 7-31ms |
| store on disk | 17.6 MB | 87.9 MB |

Against the same corpus before the three changes below: 1m20.6s to index,
1.212s per re-index, 225-435ms per search, and 419.8 MB on disk.

## Three things had to change

**A stat decides whether a file is worth reading.** Every `Index` call read
and SHA-256'd all 114 MB to find out that nothing had moved, and `Search`
calls `Index` first, so that cost was paid per query. The `files` table
already recorded `mtime` and `size` and never read them back. Comparing
those first turns the common case into one `lstat`: 1.212s to 204ms.

The hash stays the authority. A stat that differs in either field falls
through to reading and hashing, so a file rewritten to the same length is
still caught by its timestamp, and a file whose timestamp moved without its
content changing costs a hash and no reindex. What the gate cannot see is a
write that restores both the nanosecond mtime and the byte count, which
takes deliberate effort (`touch -r`, a `tar` or `rsync` that preserves
times onto identical lengths). The full-hash pass still happens whenever
either field moves and on any file the index has never seen.

**Whole file text leaves the index above a size.** A trigram index over
source runs about 3.5x the bytes it covers, so on this corpus the whole-file
rows were 390 MB of the 420 MB store, 32s of the cold index, and the
difference between a 41ms search and a 435ms one. What they buy is substring
search inside files, which `rg` answers across the same 114 MB in half a
second, from a tool already advertised, at no index cost.

So `MaxContentIndexBytes` (16 MB of claimed source) decides. Under it, file
text is indexed and a literal query gets ranked results with line matches in
one call, which no subprocess beats at that size. Over it, the index holds
symbols and paths only, and `search` says so in its own answer rather than
returning "no matches" for something that is plainly in the tree. A project
that grows across the threshold has its stale whole-file rows dropped in the
same pass, because a partial content index is worse than none: it answers
confidently from whichever files happened to be indexed first.

**A file no human wrote stays out.** `MaxFileBytes` is 256 kB. Every source
file above 100 kB in the checkouts this was measured against is generated or
a data table (an OpenAPI-generated client, a schema module, expected-output
fixtures, lookup tables), and the largest hand-written one is 89 kB. Five
files in the corpus exceed it. `vendor` sits in the skip list beside
`.venv` and `node_modules`.

## What is still on the walk

239ms of each query is `filepath.WalkDir` plus one `lstat` per claimed file.
Splitting the stats across eight goroutines takes it to 193ms, which is 19%
for real concurrency and a stat-error path that reads as a size refusal, so
it is not in. The traversal is the floor, not the stats.

Going below it means not walking on the query path at all, which means a
filesystem watcher: the kernel names what changed, `Refresh` re-checks only
those paths, and a periodic full walk backstops the events a watcher can
drop. That is a real answer rather than a cache, because it observes the
tree the way re-walking does, which is the property the freshness rule in
`codeintel.Indexer` actually asks for. It is unbuilt, and 239ms is the
number that would justify building it.

## Bounds already in place

- `MaxIndexBytesPerPass` (32 MB) bounds one pass, counting only files it
  parses, so an unchanged file costs a hash and every pass advances. The
  corpus takes four passes and the same total time as one unbounded pass,
  with the walk lock released between them
- `Start` drains those passes and holds `IndexStats.Building` across all of
  them, so a query during the first index answers from what the store holds
  and says the answer is partial
- `DefaultCoverageBudget` (10 minutes) bounds one coverage-map build, which
  costs at least 0.49s per test regardless of module size, and defers the
  rest to the next build
