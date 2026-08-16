# coverage.py dynamic contexts as a line-to-test store

Demo against `../calcipy` (pytest-cov 7.1.0, coverage 7.13.5, 22 test files, 720
statements in `calcipy/`). Commands run from `calcipy`'s repo root with
`COVERAGE_RCFILE`/`COVERAGE_FILE` pointed at this dir, so nothing is written into
`../calcipy`.

## Timings (3 runs each, `time -p`, `-p no:randomly`)

| run | real (s) |
|---|---|
| `pytest -q` (no coverage) | 3.34, 3.36, 3.42 |
| `pytest -q --cov=calcipy` (no context) | 3.92, 4.09, 4.17 |
| `pytest -q --cov=calcipy --cov-context=test` | 4.18, 4.42, 4.53 |

Coverage adds ~20% wall time; per-test dynamic contexts add another ~8% on top,
~30% over no coverage. All 88 tests passed in every configuration.

## Schema produced by coverage.py

`coverage.py` stores per-test line coverage as `(file_id, context_id, numbits)`, one
row per file/test pair, line set packed into a bitmap blob (`coverage.numbits`), not
as ranges: `file(id, path)`, `context(id, context)` ('' for the overall run, else
`"test.module.test_name"`), `line_bits(file_id, context_id, numbits)`.

`extract.py` unpacks each numbits blob with `coverage.numbits.numbits_to_nums`,
collapses consecutive line numbers into ranges, and writes Wavez's target schema:

```sql
CREATE TABLE coverage (
    file TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    test TEXT NOT NULL
);
```

Result: 122 (file, test) pairs from `line_bits` collapsed into 422 range rows in
`store.sqlite`.

## Example selections (`select.py`)

```
$ uv run select.py store.sqlite calcipy/invoke_helpers.py 30 35
tests.tasks.test_cl.test_cl
tests.tasks.test_doc.test_doc
tests.tasks.test_lint.test_lint
... (12 tests total)

$ uv run select.py store.sqlite calcipy/tasks/all_tasks.py 80 95
tests.tasks.test_all_tasks.test_all_tasks
```

## vs. pytest-testmon (not installed; from its docs)

- testmon stores its own SQLite db (`.testmondata`) keyed by a hash of each test's
  executed line *and* AST/bytecode fingerprints, so a no-op edit can invalidate cache.
- Its schema is internal to testmon's own code, not documented as a stable external
  format; querying it means importing `testmon.testmon_core`, not plain `sqlite3`.
  coverage.py's `context`/`line_bits`/`file` tables are documented and stable.
- testmon answers "which tests should rerun given this diff", not "list tests
  covering file:line-range", which is what Wavez's store needs and what coverage.py
  contexts answer directly via `line_bits` + `numbits`.
- Both need line coverage instrumentation at test time; testmon adds its own hashing
  pass on top.
- xdist: `dynamic_context = test_function` in `.coveragerc` is rejected by pytest-cov
  under xdist (`DistCovError`,
  [pytest-cov#604](https://github.com/pytest-dev/pytest-cov/issues/604)); the
  `--cov-context=test` CLI flag works under `-n 4` and combines correctly (verified:
  83 contexts, 225 `line_bits` rows). testmon does not support xdist per its docs.

## Rerun

```bash
DEMO=/path/to/wavez/_ai_/demos/code-store-python
cd /path/to/calcipy

# per-test contexts, single process
COVERAGE_RCFILE="$DEMO/.coveragerc" COVERAGE_FILE="$DEMO/.coverage" \
  uv run pytest -q -p no:randomly --cov=calcipy --cov-context=test

# under xdist: pass --cov-context on the CLI, not dynamic_context in the rcfile
COVERAGE_FILE="$DEMO/.coverage.xdist" \
  uv run pytest -q -p no:randomly -n 4 --cov=calcipy --cov-context=test

cd "$DEMO"
uv run extract.py "$DEMO/.coverage" "$DEMO/store.sqlite"
uv run select.py store.sqlite calcipy/invoke_helpers.py 30 35
```

## Verdict

coverage.py's dynamic contexts give exactly the `(file, line_range, test)` map
Wavez's store needs, in a documented, stable, language-agnostic SQLite format
queryable with plain SQL, and they work under xdist via `--cov-context`. testmon's
store is undocumented, Python-only, and answers a different question (rerun
selection, not line ownership), with no xdist support. coverage.py contexts replace
testmon for Wavez's store; testmon isn't a contender here.
