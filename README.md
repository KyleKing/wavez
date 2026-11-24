# Wavez

The deterministic layer under a coding agent. Wavez runs beside Claude Code
and does the parts of the work that never needed a model:

- **Gates** run the checks a change selects, without the agent asking. The
  edit-stage formatter is on by default. The stop-stage checks (build, test,
  lint) are opt-in with `stop_checks = true`: a failure then blocks the end
  of the turn with the lines that carry a diagnostic, and a check that could
  not run, or a test that was already failing, is reported to you and not to
  the agent
- **A goal** you write once in `.wavez/goal.toml` is checked by code at the
  end of every turn: a test passes, a pattern is present or absent, a file
  exists, a command exits 0
- **What needs you** comes first, at no token cost: one line at the end of a
  turn, a status line, and a page per session with a timeline, a waste list,
  and sentences to copy into the next prompt
- **Refactors** take a name or a line range instead of text: `wavez rename`,
  `move`, and `delete` for Go declarations, `extract` for statements,
  `params` to remove or reorder parameters at every call, and `inline` for
  calls. They are applied through `gopls` and undone if they add a build
  error. A `sed` rename of a declared Go symbol is refused once and pointed
  at `wavez rename`
- **A benchmark** runs the same pinned tasks under Claude Code alone and
  under Claude Code with wavez, interleaved, and says whether the difference
  is outside the noise

[DESIGN.md](DESIGN.md) has the reasoning, what has been measured, and what is
not built. Linux and macOS only.

## Use

```bash
go install github.com/kyleking/wavez/cmd/wavez@latest
go install golang.org/x/tools/gopls@latest   # the refactor commands need it

cd your-project
wavez init
```

`wavez init` merges five hooks into the repository's `.claude/settings.json`
and keeps its session state under `.wavez/`, which ignores itself. In a Go
module it also installs a skill that tells the agent when to use the refactor
commands. Run it again after an upgrade, and `wavez init --remove` takes the
hooks and the skill back out. The built-in Go checks are format, build, test, and lint (lint only when the
project has a `.golangci.*` file). Out of the box only format runs, after
each edit, and the agent is told once to check its work with the narrowest
command and leave whole-project runs to CI. `stop_checks = true` adds build,
test, and lint over the changed packages at the end of every turn, which a
benchmark found costs half again the wall clock.

The status line is your own choice, so init leaves it alone. `wavez
statusline --config` prints what to add to `~/.claude/settings.json`.

A goal for the task at hand:

```bash
wavez goal init "ParseGlob rejects an empty pattern"
wavez goal add test "./internal/config -run TestParseGlob_Empty" --name "error returned"
wavez goal add grep README.md "empty pattern" --name documented
wavez goal            # [x] / [ ] per condition, and n of m done
```

Set `block = true` in `.wavez/goal.toml` and the agent cannot end its turn
while a condition is unmet.

For anything else, declare checks in `.wavez.toml`:

```toml
stop_checks = false     # true: build, test, lint at the end of every turn
max_rounds = 3          # the same failure this many times releases the agent
stop_budget = "2m30s"   # how long one stop run may take in all; init sizes the Stop hook to it
announce = true         # tell the agent at session start that the gates run
route = false           # hint each prompt as direct, explore, or coordinate
disable = ["lint"]      # built-ins: format, build, test, lint

[decide]                # optional, used only with route = true
endpoint = "http://localhost:11434/v1"   # asked only when it is on this machine
model = "qwen2.5-coder:7b"               # a non-thinking model
timeout = "3s"

[[check]]
name = "ruff"
globs = ["**/*.py"]
command = "uv run ruff check {files}"
fix = "uv run ruff check --fix {files}"   # runs first
level = "block"         # or "advise"
stage = "stop"          # or "edit", which runs after every write
timeout = "2m"
```

A remote endpoint goes in your own environment as `WAVEZ_DECIDE_ENDPOINT`,
with `WAVEZ_DECIDE_API_KEY` beside it. A project file cannot name one, since
a cloned repository would then choose where your key is sent.

## Commands

| Command | What it does |
|---|---|
| `wavez init [--dir DIR] [--bin PATH] [--print] [--remove]` | Wire the hooks and the skill into a project, or take them out |
| `wavez gate [--stage edit\|stop] [--json] [files...]` | Run the checks by hand. With no files it uses git's changed files |
| `wavez goal [status\|init\|add\|clear]` | Write a goal code can check, and say how much of it is done |
| `wavez statusline [--config]` | The Claude Code status line: what needs you first, then progress |
| `wavez report [session] [--text] [--bench]` | Write a page on how a session went, or on the benchmark records |
| `wavez stats <session-id\|transcript.jsonl>` | Attribute the turns of any Claude Code session: retrieval, edits, shell, sweeps |
| `wavez rename [--dry-run] <file.go\|./pkg>:<Name\|Type.Method> <NewName>` | Rename a symbol and every reference, then list leftover mentions |
| `wavez move <file.go>:<Name>[,<Name>...] <dest.go>` | Move declarations to another file of the same package |
| `wavez delete <file.go>:<Name>[,...] [more...]` | Delete declarations, refusing while anything outside the set still uses them |
| `wavez extract <file.go>:<FROM>-<TO> <NewName>` | Move whole statements into a new function, or a method when they use the receiver |
| `wavez params <file.go\|./pkg>:<Func\|Type.Method> <name,...>` | Keep the named parameters in that order, at the declaration and every call |
| `wavez inline <file.go>:<FROM>[-<TO>] <Func>` | Replace the calls to a function on those lines with its body |
| `wavez bench list\|run\|report` | The A/B benchmark. [bench/README.md](bench/README.md) explains how to read it |
| `wavez route <prompt>` \| `route eval [--claude MODEL] <corpus.jsonl>` | Print the route decision for a prompt, or score the rules and a model on a labeled corpus |
| `wavez hook` | What the hooks call. It reads one Claude Code hook event on stdin |

## Does it help

Not on cost, so far. One paired run on Sonnet over seventeen small tasks
found turns and cost inside the noise (1.08x and 1.05x of Claude Code alone)
and wall clock 1.5x, because the stop gate runs checks the baseline skips.
The pass rate did not move. [DESIGN.md](DESIGN.md#what-has-been-measured)
has the table. So the stop checks are off unless
a project turns them on. What wavez gives by default is for you and not for
the bill: a goal code checks, a line saying what needs you, and a flag on a
skipped test.

To repeat the run, which bills a Claude account:

```bash
go build -o bin/wavez ./cmd/wavez
bin/wavez bench run --all --arm baseline,wavez,wavez-gates --model sonnet
bin/wavez bench report
```

## Non-goals

- A chat loop, a daemon, or a TUI
- A sandbox or a permission layer. The harness has its own
- Plugins. The checks are a config file and the refactors are six commands
