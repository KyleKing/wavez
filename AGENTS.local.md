# Project guidance for wavez

[DESIGN.md](DESIGN.md) says what wavez is: gates, name-based refactors, and a
benchmark that run under a coding agent. It runs no model. This file holds
the traps that are not visible from the code.

- Every claim about wavez making an agent better is settled by
  `wavez bench`, with the baseline arm beside it. A change to a gate, the
  hook output, or the skill text lands with a before and after from the same
  task, and a difference the report calls noise is not a result
- A benchmark run bills a Claude subscription. Use a small model for
  plumbing checks and say which model served a number when quoting it
- A check that could not run abstains. Never turn a missing binary, a
  timeout, or a toolchain error into a failure the agent is asked to fix
- Text a hook hands the agent is paid for on every later turn of that
  session. Say nothing on a pass and keep a failure to the lines that carry
  a diagnostic
- Package patterns passed to `go` are `./`-prefixed. An unprefixed
  `internal/x` resolves against the standard library and fails with
  `is not in std`, which reads as a broken tree
- A helper that walks paths upward stops when the parent equals the child,
  because `filepath.Dir(".")` is `"."` and never reaches `/`
- The refactor commands shell out to `gopls`, and their tests skip when it is
  not on PATH, so a green run without gopls has not tested them
- No test calls a model or the network. The harness tests use a fake `claude`
  script, and a live run is something a person starts
- A test that reads or writes user-level state sets
  `t.Setenv("HOME", t.TempDir())` first, since `internal/transcript` looks
  for sessions under the home directory
- `jj git push` fires no git hook, so `mise run jj:push` is what runs the
  pre-push checks. A checkout without jj uses git and runs
  `mise exec -- hk check --all` by hand
- `_ai_/v1/` is the first design, kept for its measurements. Nothing there is
  current
