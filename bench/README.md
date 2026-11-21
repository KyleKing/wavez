# Benchmark

This directory answers one question: does wavez make Claude Code cheaper or
faster on the same work, or not? Each task in `tasks.toml` is a prompt, a pinned
commit, and a list of checks. A run checks the commit out into a throwaway git
worktree, hands the prompt to the harness, runs the checks against what it left
behind, and appends one line to `records.jsonl`.

There are two arms. `baseline` is Claude Code alone. `wavez` is the same Claude
Code after `wavez init` has written its hooks and its refactor skill into the
workspace, with the `wavez` binary first on `PATH`. Nothing else differs: both
arms get the same model, the same allowed tools, no MCP servers, and only the
workspace's own settings (pass `--user-settings` to load yours as well).

## Running an A/B

    wavez bench list
    wavez bench run h3 --arm baseline --model sonnet --n 5
    wavez bench run h3 --arm wavez    --model sonnet --n 5
    wavez bench report h3

Run from the repository root, or point `--tasks` and `--records` elsewhere.
Each run prints one line as it finishes and bills your Claude account. A run
past `--timeout` (default 15m) is recorded with stop reason `timeout`. `--keep`
leaves the workspace behind and prints where. Keep the machine otherwise idle,
because wall clock measures the laptop as much as the harness.

Another harness joins with `--harness-cmd 'pi -p {prompt}'`. Its stdout is the
answer and wall clock is its only recorded cost.

## Reading the report

Each task gets one row per arm, harness, served model, and task definition:
runs, how many passed, and the median turns, cost, wall clock, and sweeps. A
run passes when the harness finished without error and every check held.

Sweeps are whole-project build, test, or lint commands the model ran through the
shell (`go test ./...`, bare `pytest`, `npm test`). The gates exist to make
those unnecessary, so this is the number wavez is supposed to drive to zero.

Below the rows comes wavez divided by baseline for each median. Below 1x means
wavez used less. Read the words after the number before the number:

- `inside the noise (too few runs)`: an arm has fewer than 5 runs. Run more.
- `inside the noise (between 0.5x and 2x)`: the old project measured 40 to 70
  percent run-to-run variance in turns on an unchanged tree, so a difference
  smaller than a factor of two is not evidence of anything.
- `outside the noise`: at least 5 runs per arm and at least a factor of two.
  This is the only line that counts as a result.

The report prints `no comparison` instead of ratios when the arms were served
different models, ran under different harnesses, or ran different versions of
the task, since the difference would then measure that and not wavez. The
served model comes from the harness's own accounting, not from `--model`.

## Your own sessions

`wavez stats <session-id | transcript.jsonl>` attributes any Claude Code session
the same way. The rules are one table in `internal/transcript/classifier.go`.
