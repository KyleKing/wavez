# Wavez

The deterministic layer under a coding agent. Wavez runs beside Claude Code
and does the parts of the work that never needed a model:

- **Checks** run on what a change selects. `wavez check` is the one command
  the agent is told to use: fixers first, then build, test, lint, and the
  project's own checks side by side, with a pass reused while its inputs are
  byte for byte the same. The edit-stage formatter runs by itself. The same
  checks at the end of every turn are opt-in with `stop_checks = true`: a
  failure then blocks the turn with the lines that carry a diagnostic, and a
  check that could not run, or a test that was already failing, is reported
  to you and not to the agent
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
project has a `.golangci.*` file). Out of the box only format runs by itself,
after each edit, and the agent is told once: "Check your work with `wavez
check`, not whole-project build, test, lint, or type-check commands."
`stop_checks = true` runs the same checks at the end of every turn instead,
which a benchmark found costs half again the wall clock.

`wavez check` checks what the session changed (the SessionStart hook hands
the session's id to the agent's shell through `CLAUDE_ENV_FILE`), or what
git or jj reports as changed outside a session. Build and test cover the
changed packages and their importers; lint covers only the changed packages,
since a finding in an importer would be in a file the change did not touch.
It prints one line on a pass and the failing lines on a failure, and exits 0
passed, 1 failed, 3 could not run (the reason is on stderr). A run from the
shell stops at 100 seconds unless `stop_budget` says otherwise, inside the
shell tool's two minutes.

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
commit_checks = false   # true: a commit or push waits, once, for `wavez check` to pass
warm = false            # true: run the checks in the background as files are written
locate = false          # true: tell the agent where the names in each prompt are declared and tested
brief = false           # true: hand over their source too, and the command that edits and checks at once
max_rounds = 3          # the same failure this many times releases the agent
stop_budget = "2m30s"   # how long one stop run may take in all; init sizes the Stop hook to it
announce = true         # tell the agent at session start that the gates run
route = false           # hint each prompt as direct, explore, or coordinate
disable = ["lint"]      # built-ins: format, build, test, lint
parallel = 4            # checks at once after the fixers; default one per CPU, 1 is sequential

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
inputs = ["pyproject.toml", "uv.lock"]   # what else the command reads; declaring it lets a pass be reused
exclusive = false       # true: nothing runs beside it (a shared port, database, or directory)
```

A pass is reused only for a check that declares `inputs`, and only while
every file its globs or inputs match, the files it was handed, its commands,
and PATH are what they were. `inputs = []` says the command reads nothing
else. A check without `inputs` runs every time, since its command may read
anything. Lint's pass is reused per package, keyed on the linter, go, the
linter config, go.mod and go.sum, and every file of the package and of the
module packages it imports. A failure or a check that could not run is never
reused. `WAVEZ_NO_CACHE=1` turns reuse off.

A type check for another language stays narrow by being declared narrow.
`{files}` hands a check the changed files it matched, and a check whose
tool takes a project rather than files is declared once per project, with
globs that select it only when that project changes:

```toml
[[check]]
name = "types-app"
globs = ["frontend/app/**/*.ts", "frontend/app/**/*.tsx"]
command = "cd frontend/app && npx tsc --noEmit -p . --incremental --tsBuildInfoFile node_modules/.cache/wavez.tsbuildinfo"
inputs = ["frontend/app/tsconfig.json", "frontend/app/package-lock.json"]
exclusive = true        # a type check of a large app wants the memory to itself
timeout = "5m"
```

`--incremental` with a build-info file kept outside the sources is what
makes the second run cheap: the compiler rechecks the files whose
dependencies changed shape and reads the rest back. There is no sound way to
type-check only the changed files, since an edit can break a file that
imports it, so the whole project is checked and the saving is in what the
compiler remembers. The native compiler (TypeScript 7) checks on four
threads by default, each with its own memory; `--checkers 2` or
`--singleThreaded` trades time for a smaller peak. This recipe is from the
compilers' documentation and has not been timed here on a large app.

`ruff check {files}`, `pyright {files}`, and `ty check {files}` take files
directly.

`wavez apply` makes several edits and checks them in one command. It reads
the edits on stdin, each the exact lines to replace and their replacement
under a `=== path` line, writes all of them or none, then builds and runs
the tests that reach the change, leaving lint to `wavez check`. `wavez apply
--help` shows the format. With `brief = true` each prompt that names a
symbol is answered with that symbol's source and its tests' and with how to
call `wavez apply`, so a session can go from the prompt to one command.

`--at REV` on `wavez apply` and `wavez check` works in a second copy of the
tree at that revision, kept under the user cache directory, and leaves the
checkout as it is. It is for the change that is out for review while the
checkout has moved on to the next one: a fix for a failing CI job is made
and checked against the review's own head, not against whatever is half
done in the working directory, and is committed and pushed from the lane
with `git -C`.

With `warm = true`, every write starts the session's checks in a background
process at low priority. Nothing waits on it and it says nothing: the point is what they leave in
the build, test, and lint caches, so that `wavez check` or the stop checks
read most of their answers back. A background run rewrites no file, a newer
one ends the one before it, and a stop waits for the one in flight.

With `commit_checks = true`, a shell command that commits or pushes (`git
commit`, `git push`, `jj commit`, `jj git push`) is held once while the
session's changes have not passed `wavez check`, and runs when sent again.
Under jj, which fires no commit hook, that is the check a commit meets. Under
git with a hook manager, it puts the targeted run first; to keep the hook from
repeating it across the whole project in agent sessions, set the manager's
own switch in `.claude/settings.json`, such as `"env": {"HK": "0"}` for hk,
and declare what the hook ran as a `[[check]]`, such as
`command = "hk check {files}"`.

A remote endpoint goes in your own environment as `WAVEZ_DECIDE_ENDPOINT`,
with `WAVEZ_DECIDE_API_KEY` beside it. A project file cannot name one, since
a cloned repository would then choose where your key is sent.

With [detest](https://github.com/KyleKing/detest) on PATH and a `.detest`
directory in the project, `wavez check` runs only the tests that reach the
change, from detest's trace, and falls back to the changed packages' tests
whenever detest cannot vouch for its selection.

## Commands

| Command | What it does |
|---|---|
| `wavez init [--dir DIR] [--bin PATH] [--print] [--remove]` | Wire the hooks and the skill into a project, or take them out |
| `wavez run [claude arguments...]` | Start Claude Code with a short system prompt and five tools, interactive or with `-p`. The rest goes to `claude` as written |
| `wavez check [--stage edit\|stop] [--json] [files...]` | Run the checks for what changed: the session's changes, else git's or jj's. `wavez gate` is the older name |
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

With the defaults, not on cost: the first paired runs on Sonnet found turns
and cost inside the noise, and the stop checks slowed a session down, so
they are off unless a project turns them on. What wavez gives by default is
for you and not for the bill: a goal code checks, a line saying what needs
you, and a flag on a skipped test.

Two things that are off by default do move the bill, on fifteen tasks in
Hatchet served by `claude-sonnet-5-5`
([DESIGN.md](DESIGN.md#what-the-standing-text-of-the-harness-costs) has the
tables):

- `brief = true` in `.wavez.toml` answers a prompt with the code it names
  and the tests of that code, and one command that edits and checks. Turns
  0.72x and cost 0.90x of Claude Code alone
- `wavez run` starts the session with a short system prompt and the tools
  Bash, Read, Edit, Grep, and Glob. With the brief, cost was 0.48x and every
  run passed. A session started this way has no subagents, web tools, or
  skills, and it is still Claude Code on the account `claude` is signed in
  to: `wavez run`, `wavez run -p "..."`, `wavez run --model haiku`,
  `wavez run --continue`

Wall clock moved in neither: the model writes the same edits either way.

To repeat the run, which bills a Claude account:

```bash
go build -o bin/wavez ./cmd/wavez
bin/wavez bench run --all --tasks bench/tasks_hatchet.toml --arm baseline,wavez-brief,wavez-lean --model sonnet
bin/wavez bench report
```

## Non-goals

- A chat loop, a daemon, or a TUI
- A sandbox or a permission layer. The harness has its own
- Plugins. The checks are a config file and the refactors are six commands
