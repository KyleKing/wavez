# Wavez

The deterministic layer under a coding agent. Wavez runs no model. It runs
beside Claude Code and does the parts of the work that never needed one:

- **Gates** run the checks a change selects, without the agent asking. A pass
  is silent, a failure blocks the end of the turn with the lines that carry a
  diagnostic, and a check that could not run, or a test that was already
  failing when the session started, is reported to you and not to the agent
- **Refactors** take a name instead of text: `wavez rename`, `move`, and
  `delete` for Go declarations, applied through `gopls` and rolled back if the
  result does not build. A `sed` rename across Go files is refused once and
  pointed at `wavez rename`
- **Decisions** pick one of a set that code declared: a rule first, a small
  local model second, and the old behavior when neither answers. The first
  one routes a prompt as direct, explore, or coordinate, and is off by default
- **A benchmark** runs the same pinned task under Claude Code alone and under
  Claude Code with wavez, and reports turns, cost, and wall clock side by side

[DESIGN.md](DESIGN.md) has the reasoning and what is not built. The first
design, a full agent loop with its own daemon and TUI, is archived in
[`_ai_/v1/`](_ai_/v1/DESIGN.md) with the measurements that ended it.

## Use

```bash
go install github.com/kyleking/wavez/cmd/wavez@latest
go install golang.org/x/tools/gopls@latest   # the refactor commands need it

cd your-project
wavez init
```

`wavez init` merges five hooks into `.claude/settings.json`, installs a skill
that tells the agent when to use the refactor commands, and keeps its session
state under `.wavez/`, which ignores itself. Run it again after an upgrade.
Nothing else is needed for a Go module. The built-in checks are format, build,
test, and lint (lint only when the project has a `.golangci.*` file).

For anything else, declare checks in `.wavez.toml`:

```toml
max_rounds = 3          # the same failure this many times releases the agent
route = false           # hint each prompt as direct, explore, or coordinate
disable = ["lint"]      # built-ins: format, build, test, lint

[decide]                # optional: any OpenAI-compatible endpoint
endpoint = "http://localhost:11434/v1"
model = "qwen2.5-coder:7b"
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

## Commands

| Command | What it does |
|---|---|
| `wavez init [--dir DIR] [--bin PATH] [--print]` | Wire the hooks and the skill into a project |
| `wavez gate [--stage edit\|stop] [--json] [files...]` | Run the checks by hand. With no files it uses git's changed files |
| `wavez rename <file.go\|./pkg>:<Name\|Type.Method> <NewName>` | Rename a symbol and every reference, then list leftover mentions in comments |
| `wavez move <file.go>:<Name>[,<Name>...] <dest.go>` | Move declarations to another file of the same package |
| `wavez delete <file.go>:<Name>[,...] [more...]` | Delete declarations, refusing while anything outside the set still uses them |
| `wavez bench list\|run\|report` | The A/B benchmark. [bench/README.md](bench/README.md) explains how to read it |
| `wavez route <prompt>` \| `route eval <corpus.jsonl>` | Print the route decision for a prompt, or score the rules and the model on a labeled corpus |
| `wavez stats <session-id\|transcript.jsonl>` | Attribute the turns of any Claude Code session: retrieval, edits, shell, sweeps |
| `wavez hook` | What the hooks call. It reads one Claude Code hook event on stdin |

## Does it help

Not shown yet. The live runs, one per arm on Sonnet, are in
[bench/records.jsonl](bench/records.jsonl) and prove the plumbing, not a
benefit. [DESIGN.md](DESIGN.md#first-live-runs) says what they found. Run
`mise run bench:ab <task>` with more repetitions before believing either
direction.

## Non-goals

- Running a model, a chat loop, a daemon, or a TUI
- A sandbox or a permission layer. The harness has its own
- Plugins. The checks are a config file and the refactors are three commands
