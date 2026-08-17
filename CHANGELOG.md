## v0.8.0 (2026-08-17)

### Feat

- **agent**: have a model read the diff against the task and record its objection
- **tui**: search the transcript and step matches
- **cli**: add -json so a headless run reports machine-readable numbers
- **vcs**: make the run checkpoint undoable from the thread view and the shell
- **codeintel**: build a missing codegraph index in the background
- **runtime**: start and stop llama-server for the configured local model
- **gate**: fail a test the run wrote that survives reverting the run
- **daemon**: report real spend, context, and cache numbers and name the unmeasured ones
- **codeintel**: copy codegraph call edges into the store so graph search can answer
- **agent**: compact history once a request nears the local context budget
- **tools**: expose the code-intelligence context bundle as a tool
- **mise**: add daemon task to run wavezd on demand
- **tui**: show real diff hunks, ask a line, fork, and create threads
- **gate**: add a change-scoped mutation gate
- **tools**: record edits to files a run never opened
- **gate**: run the ast-grep convention rules on every change set
- **stakes**: count blast radius from the import graph so the signal stops reading unknown
- **stakes**: score a permission prompt against the run's whole change set

### Fix

- **gate**: abstain quietly when a run wrote no test instead of demanding one
- **tui**: show the model actually serving a thread, not just an override
- **agent**: fail a turn that announces a step and takes none
- **tui**: tell an empty Home that it is empty rather than that nothing matched
- **tui**: render an unmeasured gauge as unavailable instead of zero
- **agent**: refuse to report complete when the model only offered to act
- **agent**: check the wall-clock bound before every tool call
- **codeintel**: build the index so search can answer at all
- **gate**: record what each gate examined so running nothing stops reading as a pass
- **router**: default hosted to a model that calls tools reliably
- **agent**: fail a turn that writes its tool call as text instead of reporting success

### Refactor

- **permission**: drop stakes and show the guard's reason on the prompt instead

## v0.7.0 (2026-08-16)

### Feat

- **agent**: bound a run by deadline, spend, and stagnation instead of turns
- **stakes**: score a change deterministically as evidence for the user

### Fix

- **release**: ship both binaries in one archive so the cask installs a working pair
- **config**: track the pkl schema so the project config can be evaluated

## v0.6.0 (2026-08-16)

### Feat

- **vcs**: replace git with jj and take checkpoints from the operation log
- **rules**: gate bare nolint and load the guidance wavez never saw
- **agent**: escalate on a repeated call and gate an abandoned change set

### Fix

- **app**: resolve the hosted key lazily so a local run needs no credential
- **gate**: format in process so a released binary needs no goimports
- **release**: ship wavezd alongside wavez

## v0.5.0 (2026-08-16)

### Feat

- **sysinfo**: report real memory in the diagnostics strip
- **cli**: open the interface when no prompt is given
- **tui**: home, thread, inbox, diagnostics, palette, and layered controls
- **config**: resolve the hosted key from a command instead of the environment
- **daemon**: add the wavezd binary and render thread steps as words
- **api**: add the socket client and fail fast on an overlong socket path

### Fix

- **codeintel**: quote FTS5 terms so a path in a query is not syntax
- **daemon**: name a thread from its prompt so lists are scannable

## v0.4.0 (2026-08-16)

### Feat

- **thread**: log the tool input alongside its result

### Fix

- **cli**: give each -p run its own thread unless resumed

## v0.3.0 (2026-08-16)

### Feat

- **app**: give every thread a base system prompt
- **agent**: verify the final turn against gates instead of trusting done
- **rules**: add the first ast-grep convention rule
- **daemon**: serve the socket API with lag-aware fan-out and a pending-prompt registry
- **cli**: headless -p entry point wired through the composition root
- **app**: one composition root for the headless runner and the daemon
- **config**: load .wavez.pkl with explicit context, never auto-loading agent files
- **runtime**: manage llama-server as a child process with n-gram speculation
- **astgrep**: convention-rule gate that reports unavailable rather than passing
- **api**: define the unix-socket protocol every client speaks

### Fix

- **tools**: let read take start_line without end_line
- **edit**: bound the near-match echoed back on a failed anchor
- **tools**: say that new_string replaces old_string entirely

## v0.2.0 (2026-08-16)

### Feat

- **gate**: change-triggered checks with three-tier test selection
- **vcs**: changed files and diffs against a gate-owned marker
- **agent**: streaming tool-use loop with bounded retries and loop detection
- **thread**: append-only history, deterministic compaction, and session ledger
- **tools**: read, write, str_replace, shell, search, and question tools

### Refactor

- **ci**: read the release tag from the bump job instead of rebuilding it

## v0.1.1 (2026-08-16)

### Fix

- **ci**: check out the v-prefixed tag in the release job

## v0.1.0 (2026-08-16)

### Feat

- **eventlog**: bounded append-only thread event log with lag-aware fan-out
- **codeintel**: SQLite store with tree-sitter symbols, FTS, and coverage
- **llm**: scripted fake provider, OpenAI-compatible client, and model router
- **guard**: fail-closed destructive-command guard in front of shell
- **sandbox**: Seatbelt profile rendering and sandboxed exec
- **edit**: str_replace with exact match and whitespace-fuzzy fallback
- **core**: define llm, tool, permission, and event contracts

### Fix

- **core**: order contract fields for alignment and document const blocks
