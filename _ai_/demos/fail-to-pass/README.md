# Spike: is fail-to-pass mechanically checkable

Feeds the Cycles section of [DESIGN.md](../../../DESIGN.md). The question:
can a phase's exit condition be checked by the harness rather than reported
by the model? For a fix, the condition is the one SWE-bench work calls
fail-to-pass: the test must fail before the fix and pass after.

## What it checks

`check.sh <commit>...` splits a commit's Go files into tests and code,
checks the commit out into a throwaway worktree, confirms it is green, then
reverts **only the non-test hunks** and runs the touched packages again.

Reverting the fix rather than checking out the parent is the part that
matters. Running a new test against the parent commit usually fails to
compile for reasons unrelated to the bug, which proves nothing. Reverting
only the code hunks holds everything else fixed, so a red result means the
test detects that change and nothing else.

Read another way, this is mutation testing with a single, maximally relevant
mutant: undo the fix. That is why the same machinery serves both the Cycles
exit gate and the mutation-testing gate.

## Verdicts

| Verdict | Meaning |
|---|---|
| `FAIL-TO-PASS` | The test goes red when the fix is reverted. The fix is covered |
| `LIVED` | The test still passes without the fix. The test does not cover what changed |
| `NO TEST` | The commit changed no test file, so the condition cannot hold |
| `TEST ONLY` | Nothing to revert |
| `BASELINE RED` | The commit does not pass on its own, so the comparison is noise |

`LIVED` borrows its name from the mutation-testing convention: a mutant that
survives the suite.

## Numbers

Thirty commits, `git log -30` as of `16b657f`, one run each, ~7 s per commit
on an M2 Pro:

| | Commits |
|---|---|
| Changed Go code | 19 |
| ... `FAIL-TO-PASS` | 16 |
| ... `NO TEST` | 3 |
| ... `LIVED` | 0 |
| Test-only | 1 |
| Touched no Go code (docs, release, version bumps) | 10 |

The three that changed Go code without a test are worth naming, because the
check earns its place by finding exactly these: `bcc560c` (hosted model
default, validated by external measurement rather than a test), `7f29912`
(thread naming), and `2956996` (CLI launch path).

## Limits

No `LIVED` appeared in this corpus, so the check's discriminating power is
demonstrated here only in the negative direction. It was confirmed by hand
twice during the session that produced this spike: reverting
`internal/gate/convention.go`'s relative-path fix, and disabling
`internal/gate/gotest.go`'s zero-tests guard, each turned its test red as
this check predicts. A corpus with a real `LIVED` is still wanted.

Go-only, since it shells out to `go test`. Commits that touch several
packages run all of them, so the cost scales with the change, not the repo.

## Trap worth keeping

`git diff` must be invoked with `--no-ext-diff`. With an external differ
configured (difftastic and friends), `git diff` renders for a human and
emits no applicable patch, so every revert silently produces an empty diff
and every commit reports `FAIL-TO-PASS` for the wrong reason. The first run
of this spike hit exactly that, which is itself an instance of the failure
class the session was chasing: a check that examined nothing and reported
success.
