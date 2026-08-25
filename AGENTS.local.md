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
  model can take mid-call. State alternative input shapes as a top-level
  `oneOf` of whole objects (`buildOneOf`), because an `anyOf` written beside
  `properties` is silently ignored, and never let an absent field mean
  something destructive
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
- `.wavez.pkl` injects the `Go conventions` section below into every turn, so it
  holds only what no gate answers. The `lint` gate runs `golangci-lint` on a run's
  changed files and reports what it finds, which covers naming, early returns,
  naked returns, function length, error wrapping, `errors.Is`/`As`, ignored errors,
  and doc comments on exported symbols. [AGENTS.md](AGENTS.md#go-conventions) keeps
  the full list for a human reader, and a rule that moves into the linter comes out
  of the section below

## Go conventions

- Define interfaces where they are consumed, keep them to 1-3 methods, and add one
  only once a consumer needs it
- Functional options for constructors with optional configuration (`WithTimeout(d)`)
- Define types for domain-specific errors rather than matching on strings
- Validate at boundaries and trust internal code (parse, don't validate)
- A doc comment states non-obvious behavior and invariants, never the types
- Pass dependencies explicitly
