# Benchmark

This directory answers one question: does wavez make Claude Code cheaper or
faster on the same work, or not? Each task in `tasks.toml` is a prompt, a pinned
commit, and a list of checks. A run checks the commit out into a throwaway git
worktree, hands the prompt to the harness, runs the checks against what it left
behind, and appends one line to `records.jsonl`. Runs before the fourth
brief run are in `records-2026-11-early.jsonl`, moved there to keep each file
under the size CI's hooks allow; `wavez bench report --records` reads either.

There are five arms. `baseline` is Claude Code alone. `wavez` is the same
Claude Code after `wavez init` has written its hooks and its refactor skill into
the workspace, with the `wavez` binary first on `PATH`. `wavez-quiet` is the
`wavez` arm with `WAVEZ_ANNOUNCE=0` in the harness's environment, which every
hook inherits. `wavez-gates` is the `wavez` arm with `stop_checks = true`, so build, test, and
lint run at the end of every turn. `wavez-route` is the `wavez` arm with `route = true` written into
the workspace's `.wavez.toml`, so each prompt may get a one-sentence hint about
how to start on it. Nothing else differs: every arm gets the same model, the
same allowed tools, no MCP servers, and only the workspace's own settings (pass
`--user-settings` to load yours as well).

An arm is one line of `armSpecs` in `internal/bench/arm.go`: whether it is
wired, the top-level `.wavez.toml` keys it sets, and the environment it adds.
A new variant of the wavez arm is one more line there, and each record keeps
what its arm set as `arm_env` and `arm_config`.

The route hint is answered by rules first and by the decision model only when
no rule fires. A workspace has no `[decide]` endpoint unless the pinned tree
configures one, so `wavez-route` as it stands measures the rules alone: a
prompt no rule answers gets no hint.

## Running an A/B

    wavez bench list
    wavez bench run h3 --arm baseline,wavez,wavez-route --model sonnet --n 5
    wavez bench run --all --arm baseline,wavez --model sonnet
    wavez bench report h3
    wavez bench report

Run from the repository root, or point `--tasks` and `--records` elsewhere.
Each run prints one line as it finishes and bills your Claude account. `--keep`
leaves the workspace behind and prints where. Keep the machine otherwise idle,
because wall clock measures the laptop as much as the harness.

Name every arm of a comparison in one invocation. Each repetition then runs
every task named (comma-separated, or `--all`) once under every arm, with the
arms in a freshly shuffled order for each task. Running one arm's runs and then
the other's hands the second arm the first one's warm prompt cache and whatever
else changed on the machine meanwhile. The shuffle's seed is printed and kept
in each record with the repetition and the run's position in it; `--seed`
gives an order again.

A run past `--timeout` (default 15m) is recorded with stop reason `timeout`,
and the harness's whole process group is stopped, with SIGTERM and five seconds
later SIGKILL, so nothing it started outlives the workspace. A check that runs
a program gets `--check-timeout` (default 10m); one that takes longer is a
failed check whose detail says it timed out.

Each record carries `build`, a hash of the wavez binary the run used, and
`flags`, the bounds and settings of the invocation. The version string cannot
tell builds apart, since every local build calls itself `dev`. Runs that
differ in either are never summarized together or compared, so a comparison
needs its baseline from the same build: the price of never crediting wavez
with a change to the harness. Records from before these fields were kept show
`unknown` for both and stand apart from every stamped record.

Another harness joins with `--harness-cmd 'pi -p {prompt}'`. Its stdout is the
answer and wall clock is its only recorded cost.

A task whose `repo` is a clone URL is checked out from a bare mirror under the
user cache directory (`~/.cache/wavez/bench-repos` on Linux,
`~/Library/Caches/wavez/bench-repos` on macOS), one per URL. A full commit hash
is fetched alone at depth one the first time a run needs it, and every later
run makes a worktree of the mirror without touching the network. An
abbreviated hash fetches every branch with full history instead. Delete a
mirror to reclaim the space.

## Tasks on Hatchet

`tasks_hatchet.toml` holds nine tasks on
[Hatchet](https://github.com/hatchet-dev/hatchet) at one pinned commit, a
project wavez did not write: two questions, edits and renames in its Go code,
its Python SDK, and its TypeScript SDK, and one change across three files.

    wavez bench list --tasks bench/tasks_hatchet.toml
    wavez bench run --all --tasks bench/tasks_hatchet.toml --arm baseline,wavez --model sonnet

The first run fetches the commit, about 100 MB. The checks then run offline
and need `git`, `sh`, `grep`, `python3` (3.11 or later), Node 22.18 or later
for its built-in TypeScript type stripping, and a Go that can build the
module's `go` line (1.26.10 at the pinned commit; an older Go fetches that
toolchain once, which needs the network). Each Go check builds one package that
imports only the standard library, so no module is downloaded, and takes about
20 seconds with a cold build cache. Python checks load one file by path,
because importing `hatchet_sdk` needs grpc, and TypeScript checks import one
file through Node, because type-checking needs `pnpm install`.

A tasks file may declare `[[gate]]` tables, which take the keys of a
`[[check]]` in `.wavez.toml`. Each is added to the `.wavez.toml` of every
wired arm's workspace and to the hash of every task in the file; the baseline
gets none. Hatchet's declare a syntax check for changed Python and TypeScript
files, since the built-in gates know only Go. The built-in Go gates still run
on Hatchet's Go files, and building the importers of a changed package
downloads Hatchet's modules: run `go mod download` once in a checkout of the
pinned commit before a wired run, or the first stop gate spends its budget
downloading.

## Wide tasks on Hatchet

`tasks_hatchet_wide.toml` holds six more tasks at the same commit, each a
small change in a package that between 95 and 252 of the module's 356 packages
import. Testing every importer of such a package takes longer than the check
budget, so these are the tasks where an agent is tempted into a whole-project
run and where `wavez check` needs test selection to answer at all.

Selection reads a [detest](https://github.com/KyleKing/detest) trace of the
pinned commit, which the pinned tree does not carry. Make one, once, and hand
it to the wired arms with `--overlay`, a directory whose files are copied into
each wired arm's workspace before wavez is wired in:

    git clone https://github.com/hatchet-dev/hatchet.git && cd hatchet
    git checkout 290556fb419b76b39a6a42264600c0130edc4b1b
    go mod download && detest init && detest trace   # about 5 minutes on 4 cores
    mkdir -p ../overlay/.detest/cache
    cp .detest/detest.toml ../overlay/.detest/
    cp .detest/cache/trace.json ../overlay/.detest/cache/
    printf '*\n' > ../overlay/.detest/.gitignore

    wavez bench run --all --tasks bench/tasks_hatchet_wide.toml \
        --overlay ../overlay --arm baseline,wavez --model sonnet

`detest` has to be on `PATH`, and so does a `golangci-lint` that accepts the
module's Go version, or the lint check abstains in the wired arm. The overlay
is hashed into each record's `flags`, so runs with different traces are never
pooled, and a file the pinned tree already has is refused. The baseline gets
no overlay. A trace is a nightly cost in a project that keeps one, and no
record counts it.

## Reading the report

Each task gets one table per setup (harness, served model, task definition,
build, and flags) with one row per arm: runs, how many passed, how many failed
or timed out, and the median turns, cost, wall clock, and sweeps. A run passes
when the harness finished without error and every check held.

The medians are over the runs the harness completed. A run that timed out or
failed stopped wherever it happened to be and usually reports no turns and no
cost at all, so counting it would make an arm that fails look cheap. Those runs
are in the `failed` and `timeout` columns and in the pass count, and nowhere
else.

Sweeps are whole-project build, test, or lint commands the model ran through the
shell (`go test ./...`, bare `pytest`, `npm test`). The gates exist to make
those unnecessary, so this is the number wavez is supposed to drive to zero.

The last two columns are totals over the arm's runs, not medians, read from the
trace the hooks of a wired arm write beside the workspace. `hints` is how many
prompts were given a route hint. `denials` is how many shell commands the
PreToolUse hook refused because they renamed a Go symbol with `sed`, `perl`, or
`gofmt -r`, each time naming the `wavez rename` line to run instead. A denial
with no `wavez rename` in the transcript after it means the agent sent the same
command again, which the hook lets through.

Below the rows comes each other arm divided by the baseline, one block per arm
and one line per metric: the ratio of the medians, how many runs of each arm
measured the metric, and `p`. `--against wavez` divides by another arm instead,
which is how two wired arms are compared. Below 1x means the arm named first
used less. Read the words after the numbers before the numbers:

- `no verdict: fewer than 5 measured runs in an arm`: run more. Fewer than
  five runs an arm cannot reach the threshold however far apart they are.
- `inside the noise`: the two arms' runs could have come from one pile. The
  old project measured 40 to 70 percent run-to-run variance in turns on an
  unchanged tree, so a ratio alone is evidence of nothing.
- `outside the noise`: at least 5 measured runs per arm and `p` at most 0.05.
  This is the only line that counts as a result.

`p` is from the Mann-Whitney rank-sum test, two-sided: the chance of two arms
interleaving this unevenly when sorted together if the arm made no difference.
It is counted exactly up to 20 runs in all and approximated above that. It
looks at the spread and the number of runs as well as the gap, so a quarter
fewer turns on tight runs is a result and half as many on scattered ones is
not. Each metric is tested alone and nothing corrects for looking at four, so
one `outside the noise` among many lines is weaker than it reads.

The report prints `no comparison` instead of ratios when two arms never shared
a setup, and names what differed, since the difference would then measure that
and not wavez. The served model comes from the harness's own accounting, not
from `--model`.

### Across tasks

Five runs an arm on one task cannot show an effect of a fifth at this
variance. A report of every task therefore ends with a summary across tasks
for each pair of arms, which is where an effect of that size can show. Each
task both arms completed under one setup gives one line: the arm's median
divided by the other's for turns, cost, and wall clock, and the difference in
median sweeps. Each task is its own control, so raw numbers are never pooled
across tasks and a task that costs ten times another weighs the same.

Under the lines come the pass counts of both arms, and for each metric the
geometric mean of the per-task ratios (the mean difference for sweeps), the
number of tasks, and `p` from a paired sign-flip permutation test: the share
of all the ways of swapping the arms within tasks that gives a mean at least
as far from no difference. It is exact up to 20 tasks and sampled with a fixed
seed above that. The words are the same, with `outside the noise` needing `p`
at most 0.05 and at least 6 paired tasks, the fewest that can reach it. When
the arm that spent less also passed a smaller share of its runs, the line ends
`but not a win`, because doing less of the work is the cheapest way to spend
less.

The budget goes further on many tasks at one repetition than on many
repetitions of one task. Keep the per-task blocks for looking closely at one
task.

## The route corpus

`route_corpus.jsonl` holds labeled prompts for the route question, one
`{"state": ..., "want": ...}` a line. A state is a prompt followed, when the
prompt names paths that exist, by a blank line and `existing-paths:` with those
paths (a directory ends in a slash); `want` is `direct`, `explore`,
`coordinate`, or `silent`. The first seventeen lines are the prompts of
`tasks.toml`, with the paths that exist at their pinned commit.

    wavez route eval bench/route_corpus.jsonl
    wavez route In internal/bench/report.go print the arm name first

The first prints how many lines the rules answered, how many the configured
decision model answered, and each one that came out wrong. A rule that is wrong
on any line is narrowed until it is not, and a test in `internal/route` holds
that. The second prints the route and hint one prompt would get in this
repository.

## Your own sessions

`wavez report --bench` writes the records as a page, one bar per arm and
task. `wavez report` does the same for one session: a timeline, where the
turns and tokens went, and a waste list.

`wavez stats <session-id | transcript.jsonl>` attributes any Claude Code session
the same way. The rules are one table in `internal/transcript/classifier.go`.
