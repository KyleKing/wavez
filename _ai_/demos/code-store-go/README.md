# code-store-go: one SQLite store for Go test selection

Demo target: [`gh-repo-dashboard`](/Users/kyleking/Developer/kyleking/gh-repo-dashboard) (15 packages, 522 tests, ~10k LOC Go TUI app).

## 1. codegraph (off-the-shelf)

Installed via the shell installer (`install.sh`, prebuilt Rust binary, no compile) in under 2 minutes, mostly download time. `codegraph init .` indexed the repo in **763ms** (169 files, 3,037 nodes, 9,439 edges), stored at `.codegraph/codegraph.db` (10MB SQLite).

Schema (`codegraph_schema.sql` in this dir): `nodes` (function/method/struct/etc, each with `file_path`, `start_line`, `end_line`, symbol-level rather than file-level), `edges` (`kind` in `calls`, `contains`, `references`, `instantiates`, `imports`, `implements`, `extends`), plus FTS5 full-text search over symbols. So codegraph gives more than file-import edges: it resolves calls and references between symbols too.

`codegraph affected internal/vcs/operations.go` (**~110ms**) returned **zero** affected tests, despite reporting `totalDependentsTraversed: 104`. Root cause: its default test-file auto-detect glob doesn't match Go's `_test.go` convention (it looks JS/TS-shaped). Passing `--filter "**/*_test.go"` explicitly fixes it:

```
codegraph affected internal/vcs/operations.go --filter "**/*_test.go" --json
# 57 of 522 test files, 96 dependents traversed, ~110ms
codegraph affected internal/models/repo.go --filter "**/*_test.go" --json
# 67 of 522 test files (nearly the whole suite; repo.go is imported almost everywhere)
```

Two findings worth carrying into the design doc:
- codegraph needs `--filter` for Go repos out of the box; the auto-detect is a footgun.
- Its `affected` traversal is **file-level** (via `imports` edges between files), same granularity as a hand-rolled importer graph. It does not use its own symbol/line-level `nodes` table for selection, so a widely-imported file (`models/repo.go`) triggers >85% of the test files regardless of which function actually changed.

## 2. Line-to-test coverage store (this demo)

`buildstore/main.go` builds two tables from stock Go tooling with no third-party deps:
- `imports(src_pkg, dst_pkg)` from `go list -json ./...` (`Imports`)
- `coverage(file, start_line, end_line, test)` by running `go test -run '^Name$' -coverprofile=...` once per test, scoped to that test's own package, with an 8-worker pool, then parsing each coverprofile's per-statement blocks (`count > 0`) into rows
- `file_pkg(file, pkg)` (helper table, not asked for, needed to make the importer fallback work) mapping each Go file to its package

Cost on this repo: **522 tests, 15 packages, 248.9s wall clock with 8 parallel workers, 0 failures** (vs. ~9.3s for one `go test -coverprofile ./...` run with no per-test attribution, a ~27x slowdown). Average 0.48s per test; the outliers are `internal/vcs`'s integration tests, which shell out to real `git`/`jj` subprocesses (`git_integration_test.go`, `git_exec_test.go`), not the coverage instrumentation itself. The overhead is dominated by `go test` process/link startup repeated once per test.

Row counts: `imports` = 162 rows (15 packages × their direct imports, including stdlib/external), `file_pkg` = 162 rows, `coverage` = 21,566 rows (covering 507 of the 522 listed tests, the rest are pure error-path or skipped tests with no unique statement blocks).

## 3. Store schema

See `schema.sql`. Three tables, no ORM: `coverage`, `imports`, `file_pkg`. Loaded from the buildstore CSV output with `sqlite3 store.sqlite < schema.sql` + `.import`.

## 4. select.sh

```
./select.sh internal/vcs/git.go 126 144        # GetCurrentBranch
./select.sh internal/vcs/operations.go 26 28   # IsDefaultBranchName
```

Queries direct line coverage first (`coverage.start_line <= end AND coverage.end_line >= start`); if nothing matches, falls back to a `WITH RECURSIVE` walk over `imports` (reversed: who imports this package, transitively) joined back through `file_pkg` to `coverage`, returning any test that exercises any file in an importing package. That fallback query reproduces exactly the same importer-package set codegraph's `affected` command found for `internal/vcs` by traversing its own `imports` edges (both land on the same 7 packages: `vcs`, `cmd`, `app`, `batch`, `cli`, `discovery`, `github`), confirming the two approaches agree at file/package granularity; they only diverge once line-level coverage data is available.

**Example 1** (`GetCurrentBranch`, lines 126-144): direct coverage narrows to 5 tests, all specific to that function or its callers:

```
TestGitCleanupMergedBranches
TestGitCleanupMergedBranchesGuards
TestGitCleanupMergedBranchesSquashMerged
TestGitGetCurrentBranch
TestGitGetRepoSummary
```

**Example 2** (`IsDefaultBranchName`, lines 26-28): 3 tests, including one in the `jj` backend (`TestJJCleanupMergedBranches`) that calls the same shared helper:

```
TestGitCleanupMergedBranches
TestGitCleanupMergedBranchesGuards
TestJJCleanupMergedBranches
```

Contrast with the fallback: a change to `internal/vcs/mock.go` (a file with no test hitting it directly, since it's a test double consumed only via injection) falls back to all 7 importer packages and returns **383 tests**, most of the suite. That is the cost of file/package-level selection on a widely-imported file. Line-level coverage keeps both real examples above under 5 tests.

## 5. Recommendation

Importer-based (file/package) selection alone is not enough for v0.1: on this repo it already returns most of the suite for any change to a widely-imported file (`models/repo.go`, `vcs/mock.go`), which is close to "just run everything" and defeats the point of selection. The two real examples above show line-level coverage cutting a 500+ test suite to 3-5 tests for a typical single-function change, which is the actual value proposition.

Build the coverage loop from day one, but make it incremental rather than a full rebuild: track a content hash per source file (already free from `go list -json`'s build ID, or a simple SHA of the file), and on each store update, only re-run tests whose `coverage.file` rows include a file whose hash changed. A change to `internal/vcs/git.go` alone triggers re-running the ~15 tests already known to cover files in that package, not all 522. First build stays the expensive step (once per clone, cacheable in CI), and steady-state updates track normal PR diff size.
