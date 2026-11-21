# Project guidance for wavez

[DESIGN.md](DESIGN.md#starting-a-session) says how a session starts: what to
take from Next, jj for every write, one commit per lane, push only at a
milestone, and a PTY run for any TUI change. This file holds the traps that
are specific to this codebase and not visible from the code.

- One `wavezd` per laptop listens on `<os.UserConfigDir()>/wavez/d.sock`.
  Anything that starts a daemon for a test, demo, or PTY run passes
  `-socket <scratch path>` so it does not take over the socket the daemon in
  daily use owns
- `config.UserDir()` follows `os.UserConfigDir()`, which ignores `XDG_*` on
  macOS and resolves to `~/Library/Application Support`. A test that reads or
  writes user-level state (snippets, links, model settings) sets
  `t.Setenv("HOME", t.TempDir())` first, or it edits the real files
- Evaluating pkl starts a `pkl server` subprocess. Load config once at the
  edge (`cmd/`, `internal/app`) and pass the result in. A loader inside a
  constructor that tests call per case spawns one evaluator per test and
  trips `contextcheck`
- Bubble Tea `Update` paths must terminate on any input. A helper that walks
  paths upward (`filepath.Dir`) has to stop when the parent equals the child,
  because `filepath.Dir(".")` is `"."` and never reaches `/`. That loop hung
  the whole `internal/tui` package once
- TUI golden frames live in `internal/tui/testdata/*.golden`, rendered with
  `Options{NoColor: true}` so the bytes are stable. Regenerate with
  `go test ./internal/tui -update` and read the diff frame by frame
- A daemon reopens every log under `.wavez/threads/` when it loads a
  project, so a scratch daemon over this checkout shows the dogfood threads
  without a model. `n` then `Enter` with no prompt adds an idle thread
  without a turn
- The hk steps need mise's tools on PATH. `hk check --all` from a shell
  without `mise activate` aborts on `ls-lint: command not found` and every
  later step reads as aborted; run it as `mise exec -- hk check --all`
- Two laptops push to `main` and a `feat:`/`fix:` push cuts a release, so
  `jj git fetch` and re-read Next before taking a lane and again before
  pushing. One session rebuilt three landed items from a stale base
- On the 24 GB M4 Pro a 16 GB-class model (`qwen3.8:27b`) serves only with
  `-np 1` and a 12k window under the default Metal wired limit (18,186 MiB);
  llama-server's auto slot count and any window from 16k up fail on the
  first request with `kIOGPUCommandBufferCallbackErrorOutOfMemory`. The
  machine then sits near 10% free, so nothing else heavy runs beside it
- Harness and pull processes that must outlive a tool call need
  `nohup ... < /dev/null & disown`; a plain `&` inside the call's subshell
  dies with it. `ollama pull` resumes from its partial blob
- A thread's tier pin is `route`'s `Override`, not `new`'s `Model`. `Model`
  names a model, reaches `ThreadInfo` and nothing else, and a client that
  pins there watches every turn route to the default tier
- A lane keeps the step it died on, so a failed thread reads as working
  forever on the schedule. Anything waiting for threads to settle reads
  `ThreadInfo.State` (`done`, `failed`, `idle`) rather than the lane
- `.wavez/index.db` and `.wavez/coverage-manifest.json` are ignored by
  version control, so a fresh workspace rebuilds both. `-replay` seeds them
  from the project now (`seedDerivedState`); anything else that opens a
  scratch workspace over this repo should, or its first gate round runs a
  per-test coverage sweep of the whole module while the model waits
- `rename` goes through gopls, so it needs the file indexed and the module
  to typecheck. Its `path` narrows by prefix and takes a package directory
  as readily as a file. A rename that gopls refuses (a keyword, a symbol in
  a dependency) comes back as a tool error, not an empty success
- `delete` reaches functions, methods, and types, because that is what the
  index extracts; a field, var, or const is `str_replace`'s work. It refuses
  a declaration the language server says is still used, so removing a
  function and its tests means naming them in the same call, and the refusal
  lists the declarations holding the uses for exactly that purpose
- A replay measures the laptop as much as the tree. One `e2` lane recorded 2
  turns and a deadline at 68 output tokens in 180 seconds because
  `hk check --all` and `go test ./...` were running beside it; the same lane
  on an idle machine finished in 13 turns with every check passing. Start a
  replay, then stay off the CPU until it records, and read output tokens per
  second before reading turns
- A gate failure that names no changed file now carries the head of what
  the command printed. If that head shows a toolchain error rather than a
  source diagnostic (`package internal/x is not in std`, a missing binary),
  the gate's own command is wrong and the tree is fine: read
  `gate.Selection` before reading the code
- The web tools reach the network from `internal/web` and nothing else
  does. A test there must not depend on a live site; the live check is a
  scratch test run by hand and deleted, because a network test in CI fails
  for reasons that have nothing to do with the change
- A tool's JSON schema is a grammar on the fast tier, not documentation. A
  local turn decodes tool arguments under a grammar `llama-server` compiles
  from the schema, so any property left out of `required` is an exit the
  model can take mid-call, and an absent field must never mean something
  destructive. State one shape and never alternatives. A top-level `oneOf`
  binds on the fast tier where an `anyOf` beside `properties` is ignored, and
  it is unreachable everywhere else: GLM-5.3 answers any top-level `oneOf` or
  `anyOf` with `{}` in six completion tokens, so `openaic.schemaFor` sends
  z.ai the first branch alone. Where a tool serves one input and many, take
  the list as the only shape (`str_replace`'s `edits`, `document`'s `docs`),
  which costs a single call about ten tokens of array syntax and costs a
  branch nothing, because there is no branch. The 2026-09-03 ruff lane is why:
  the hosted tier could not see the batch branch, so it wrote its own batch
  editor in Python and ran it through `shell`, and nine calls wrote source
  files while recording no change between them
- A replay record's `model` is a tier name, and `served` is what actually
  answered it. Moving the fast tier from the loopback llama-server to a
  hosted endpoint keeps the tier name and changes the machine, the window,
  and whether tool arguments decode under a grammar at all, so a comparison
  across that move measures the move. `SameSetup` reads `served` and the
  report names the tier that moved
- `wavez -preamble` is the deterministic metric for anything the replay
  harness cannot resolve. Every new tool's cost is one run of it, and the
  pair of web tools came to 221 tokens against an estimate of 1,500, so
  estimate nothing here that can be measured in a second
- A thread's model-visible transcript is `.wavez/threads/<id>.history.jsonl`,
  not the event log beside it. The log truncates tool inputs and stores
  assistant text as streamed chunks, so anything that needs what the model
  was actually sent reads the sidecar. Copying or moving a thread means both
  files, and the daemon loads every sidecar under `.wavez/threads/` at
  startup
- `wavez -recall <thread>` repeats one recorded tool call, replaying every
  call before it so the tree is the one that call met. It reads the sidecar,
  so a thread whose turns arrived as `<function=...>` in the message body
  before that recovery moved ahead of the transcript write shows those turns
  with an empty `tool_calls` and a tool result under nothing, and its prefix
  cannot be rebuilt. Read the sidecar first when a recall's trail is shorter
  than the run was
- The Seatbelt profile allows writes under the project root and the session
  temp dir and nowhere else, so `shell` reads a declared `extraDirs`
  directory and cannot write one. A heredoc into a sibling repository comes
  back as a bare `PermissionError` naming the file and not the boundary. The
  edit tools are the only way in, which is what makes undo reach the work,
  since they record a checkpoint per repository. The project's own gates
  abstain on those changes and log the abstention as `scope`
- Fuzzy `search` ranks a wider window than it answers with, so what the
  index scores and what a caller sees are different orders. Nothing may ask
  a ranked query whether a name exists: `internal/finish` did, and
  `codeintel.Store.DeclaresName` is what that question goes through now
- The index holds functions, methods, and types and nothing else, so a
  const, a var, a struct field, a pkl key, and a tool name are all absent
  from it while being written all over the tree. Any check that reads
  "absent from the index" as "does not exist" is wrong for all five
- A thread's sidecar ends in `.jsonl` like its event log, so anything
  scanning `.wavez/threads/` has to skip `thread.HistorySuffix`. The daemon
  did not, opened each sidecar as a thread, gave that thread a sidecar of
  its own, and grew the list by one file per thread on every start: a real
  directory reached 1,246 threads for 393 logs. A checkout that ran an old
  daemon still holds the `*.history.history.jsonl` files, which are safe to
  delete
- `.wavez.pkl` injects the `Go conventions` section below into every turn, so it
  holds only what no gate answers. The `lint` gate runs `golangci-lint` on a run's
  changed files and reports what it finds, which covers naming, early returns,
  naked returns, function length, error wrapping, `errors.Is`/`As`, ignored errors,
  and doc comments on exported symbols. [AGENTS.md](AGENTS.md#go-conventions) keeps
  the full list for a human reader, and a rule that moves into the linter comes out
  of the section below
- `jj git push` fires no git hook, so every guard `hk` installs on pre-push
  (`ci`, `verify-released`, `commitizen-branch`) is inert in this checkout.
  `mise run jj:push` runs them as `jj:verify` before it pushes, and a push
  made any other way has run none of them
- `copier update` reads this checkout through git, so the colocated `.git` is
  what makes it work at all and a `jj git clone` without `--colocate` could
  not be updated. It refuses a dirty destination, and jj's auto-snapshot puts
  working-copy changes in front of git as unstaged modifications, so commit
  with `jj commit` first and never `git stash`, which fights the snapshot.
  The patch lands as `git apply --reject` into the working tree and jj
  snapshots it, so the `.rej` files arrive in the working copy. Copier
  excludes every git-ignored path from the patch, which is silent
- One `agent.Loop` serves every thread, and so does the one `ChangeGate` and
  `gate.Runner` under it, so anything per-run there has to be keyed by the
  writer. A `tool.Change` carries the thread that wrote it, stamped in
  `run.gateChanges` because the tool registry is built once per project and
  the tools have no thread of their own. `ChangeGate.Changed`, `Status`, and
  `Covers` take that writer as an argument, read off the tool call's context
  where `tool.WithWriter` stamped it, because the context is the only thing
  reaching a tool that the shared registry did not build. An empty writer is
  the every-writer answer a fixture or a one-off probe expects, so a new
  caller that forgets to pass one is wrong quietly rather than loudly
- Two lanes running beside each other is how the shared-state defects show
  up, and they read as the lane's own fault. One lane was handed the other's
  compile errors under "Gates ran on your changes", edited the other's
  brand-new file to fix them, and was then stopped by `tree_state` over the
  result. Read `TakeFeedback`'s writer before reading the code a lane
  touched
- A pty in canonical mode echoes a keystroke the moment it is written, so
  the screen is quiet on that echo before the program has been scheduled:
  measured, the echo lands at 0 ms and an answer at 407 ms. `settle` reads a
  draw as the program's only when it is not the echo of the key just sent
  (`ptyScreen.note`), which is why `expect` must be called before every
  write to the tty. A key drawing nothing beyond its echo holds the call for
  `ptyAnswerWait` (2 s), and that window is measured from the write so the
  two waits one key gets do not each spend it

- A pty fixture must not pause longer than `ptySettle` (250 ms) between two
  writes. `settle` reads a quiet screen as a program that has finished
  answering, so a gap wider than the window ends the wait while the program
  is still sleeping, the program is killed, and everything after the gap
  never lands. `TestPTY_AnswersATerminalQuery` slept 200 ms against that
  250 ms window and failed under load in two sessions, passing every time on
  an idle machine. Reproduce with 24 CPU burners beside `-count=10`
- A TUI check runs in a named tmux session and is killed when the screen has
  been read (`tmux new -d -s wz-check ...`, then `tmux kill-session -t
  wz-check`). [AGENTS.md](AGENTS.md#tui-testing) carries the rule and
  my_go_template renders it. A detached session outlives the check, and a
  Textual app whose terminal goes away spins at 100% CPU rather than exiting
- `wavezd` sweeps leftover spawned processes at startup from a record beside
  its own socket, so a daemon started on a fresh path sweeps nothing. Reusing
  one scratch socket across runs (`/tmp/wz-vcr/d.sock`) is what makes the
  sweep reach the previous run's leaks, and it is also how a stale daemon
  goes unnoticed: check `pgrep -fl wavezd` before assuming a socket is free
- Pointing wavez at a new project costs three things before the first turn
  lands, and each fails differently. Without a colocated jj repository the
  run dies in 200 ms capturing its checkpoint. Without a `.wavez.pkl` every
  tier falls back to an empty `baseURL` and the first turn is a 401. Without
  `checks` the only gate that speaks anything but Go is `lsp`, which starts
  `ty server` for Python, so a project in another language gets type errors
  and no lint, no format, and no tests
- A `shellAllow` entry is the only allow-list keyed by program name.
  `permission.Store` records one whole command line on purpose, so answering
  a prompt for `uv run ruff check a.py` does not cover the same command with
  a different `tail -n`, and a project whose checks live in a virtualenv asks
  once per variant until the program is named in `shellAllow`
- A lint rule that fights the design loops the run rather than stopping it.
  `no-self-use` fires on every method implementing an interface, and
  `typing.override` needs 3.12, so a run on a 3.11 project alternated between
  the two for ten minutes. Read what a gate is actually complaining about
  before assuming the code is wrong
- The Seatbelt profile is the fence and `shellAllow` is ergonomics. Measured:
  a script written into the project root and run through an allowed
  interpreter had every dangerous syscall denied, so the allowlist decides how
  often a run is interrupted and never what it can do. The one thing
  indirection did defeat was the protected-path rule, which the profile now
  denies on the syscall (`.wavez.pkl`, `hk.pkl`, the mise configs and tasks,
  `.github/workflows`, `.wavez/approvals.jsonl`, and any `.git` or `.jj` under
  the root). Read [DESIGN.md](DESIGN.md) for the two decision records
- A deny placed under the project root reaches the session dir too, because
  `.wavez/sessions/session-*` is under it. uv marks its sdist cache with a
  file literally named `.git`, so the first version of that deny made every
  `uv` command in a sandboxed run fail with `Failed to initialize cache`
  before anything ran. The profile re-allows the session dir after the deny,
  and `TestExec_WritesUnderTheSessionDirAreNotProtectedPaths` only reproduces
  it when the session dir is built under the root the way the real one is
- Reads are denied across `$HOME` and allowed back for the project, the
  session dir, the toolchain caches, and `extraDirs`. Each ancestor of an
  allowed path is allowed as a `literal`, not a `subpath`, so an upward walk
  can stat each level without reading into it: pytest walks up looking for
  its rootdir and dies with a bare `PermissionError` naming the ancestor
  otherwise. A sibling repository is now unreadable until the project names
  it in `extraDirs`
- `ecosystems { "python" }` is the bundle form of `shellAllow`, and the pkl
  schema is a union of the names so a typo fails the load rather than
  expanding to nothing. A project's toolchain also lives at a path
  (`.venv/bin/ty`), and reading a compiled binary before running it answers
  nothing, so a program the project vouched for by name runs from a project
  path without an approval
- A language server resolves modules once at startup. `uv add sqlglot`
  mid-run left every import of it reported unresolvable, and the run spent
  twenty turns and a spend cap investigating an environment that was fine.
  `lsp.Pool` restarts a server when its `Manifests` change, so a new
  dependency costs one restart rather than a wrong diagnostic forever
- A stage's words carry its redirects in source order, so a rule reading
  arguments sees a redirect target as one unless it calls `argsOnly`.
  `rm -rf .wavez/scratch 2>/dev/null` read as an rm of `/dev/null` and was
  refused
- A thread parked on a question loses the provider's prompt cache, and the
  answer's first turn then pays full input price on the whole context.
  Measured over 421 turns of this project's logs: every request that hit the
  cache followed a gap of 352 seconds or less, every request that missed it
  followed a gap of 736 seconds or more, and seven cold turns re-read 510,696
  input tokens. `agent.DefaultCacheLifetime` (10 minutes, inside that gap)
  makes a run compact before asking again, because the cache is gone anyway
  and a smaller prefix is what it re-reads. Compacting while the cache is
  still warm rewrites part of the prefix and costs rather than saves, which is
  why the default sits on the cold side
- A review round on a long-lived thread costs far more than the same round in
  a fresh one, for the same reason. Thread `52735e97` reached 114,735 input
  tokens, sat 13 minutes waiting for an answer, and spent the rest of a $4
  ceiling in the turns after it without landing a change. Hand a review back
  as a new thread with the findings restated, and keep `-resume` for a thread
  that is still warm
- A reasoning model spends its completion budget on reasoning before any
  content, so a `maxTokens` sized for the answer alone comes back empty with
  `finish_reason: "length"`. Every review this project ever ran failed that
  way: measured against glm-5.3 on a 20 KB diff, 200 tokens went entirely to
  reasoning. `collectText` now reports truncation separately, because a
  truncated answer and a refused one both arrive as empty text and the two
  want different fixes

## Go conventions

- Define interfaces where they are consumed, keep them to 1-3 methods, and add one
  only once a consumer needs it
- Functional options for constructors with optional configuration (`WithTimeout(d)`)
- Define types for domain-specific errors rather than matching on strings
- Validate at boundaries and trust internal code (parse, don't validate)
- A doc comment states non-obvious behavior and invariants, never the types
- Pass dependencies explicitly
