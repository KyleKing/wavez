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
