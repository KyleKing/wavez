## v0.18.0 (2026-09-01)

### Feat

- **runtime**: size the llama-server prompt cache from admission headroom

### Fix

- **gate**: keep one lane's gate findings from reaching another
- **gate**: key a run's lint baseline to the thread that wrote the change

## v0.17.0 (2026-09-01)

### Feat

- **gate**: tell a run's own lint findings from what it inherited
- **tool**: declare each tool's risk class and read it in one place
- **tui**: page a run's timeline instead of scrolling it
- **gate**: stop gating a change this project's repository does not hold
- **daemon**: undo every repository a thread edited
- **search**: name a path under a declared extra directory as outside the index
- **config**: let a project set how long one run may take
- **tools**: let a project's declared extra directories actually be reached
- **daemon**: scope an undo to a thread's own paths while another lane writes
- **gate**: name a lint finding another writer left instead of hiding it
- **gate**: route a failure a run cannot have caused to the scheduler
- **runtime**: cap llama-server's host prompt cache with --cache-ram

### Fix

- **tui**: advertise the timeline's cursor keys in its footer
- **shell**: name the sandbox write boundary behind an Operation not permitted
- **guard**: stop reading the 1 of 2>&1 as a command needing approval
- **lease**: count only the lanes writing the tree being asked about
- **daemon**: read what actually served each turn off the thread's log
- **daemon**: bind the socket before announcing it

## v0.16.1 (2026-08-31)

### Fix

- **scripts**: generate the tap deploy key inside 1Password

## v0.16.0 (2026-08-31)

### Feat

- **gate**: record a lint finding on a neighbor instead of dropping it
- **guard**: refuse every tool write to the files that govern the guard
- **search**: say when a project is too large for the file-text index
- **codeintel**: bound one index pass so a large tree still reaches the incremental path
- **gate**: bound one coverage build and let the next take what it deferred
- **codeintel**: answer from what is indexed while the first pass is still walking
- **codeintel**: pass over vendored trees and files no human wrote
- **annotate**: hand an image to the user to mark up and read what they drew
- **permission**: narrow the approval key and persist allow-always per project
- **finish**: check that a run which wrote nothing read what it names
- **bench**: print a run as one line per turn with -timeline
- **routine**: fire the schedule and thread-lifecycle triggers
- **routines**: mark a step that examined nothing as an abstention
- **read**: give each path in a batched read its own line range
- **home**: archive threads and read the archive as its own list
- **routine**: declare a service and hold it only while a step needs it
- **tools**: drive a program under a real terminal and read what it drew
- **shell**: keep the whole output of a trimmed command where the run can read it
- **tools**: look at an image and answer one question about it
- **config**: give a turn that carries an image its own tier
- **codeintel**: index stylesheets and markup templates
- **llm**: carry image content parts, and say which tiers accept them
- **codeintel**: index TypeScript and TSX
- **gate**: let a project declare its own change-triggered checks
- **tools**: outline a long file instead of returning its text
- **tui**: give a key its phrase, and fold the help list into columns
- **tui**: fold a transcript row to its headline and keep the body for the expansion
- **tui**: select Home rows with space and `*`, and answer a selection at once
- **tui**: filter Home by state with a `state:` term in the query
- **agent**: shadow-price the GLM tiers at z.ai pay-per-use rates
- **llm**: serve the network tiers from z.ai instead of OpenRouter

### Fix

- **guard**: protect every file mise loads a config or a task body from
- **pty**: wait for the program to answer a key rather than for its own echo
- **search**: scope a query in the index rather than filtering its answer
- **agent**: report a cancellation that lands during a run's startup as one
- **pty**: make each wait watch its own quiet window
- **pty**: let the reader drain before closing the terminal under it
- **pty**: end the wait when the program exits, not on its own echo
- **pty**: wait for a slow program to draw before reading its screen
- **agent**: run the finish checks on a run that ended on a bound
- **search**: say a miss is an absence from the index, not from the tree
- **codeintel**: keep a project's dependencies out of the index
- **gate**: abstain from the build gate outside a Go module
- **prompt**: say the gates run on every change, not at the finish
- **shell**: answer a package sweep the gates already ran over
- **shell**: refuse an in-place stream edit and name the tool that makes it
- **finish**: stop reporting a const, a field, or a config key as invented
- **tools**: refuse a rename already refused with the same words
- **replay**: say when no run of a task version has ever passed a check
- **gate**: give the linter a cache per root and let it wait for the lock
- **tools**: refuse an anchor already refused for a file that has not changed
- **gate**: match a lint finding by shape and path suffix, not by substring
- **tools**: finish a rename whose declaration a run already hand-edited
- **tui**: window the schedule list and let a lane name take the row
- **gate**: lint the packages a change touches, not the files alone
- **tui**: size the transcript from the rows around it, so the footer stays on screen
- **tui**: close the goal overlay on esc instead of popping the screen under it
- **llm**: send z.ai one schema branch, since GLM answers a composed one with {}

### Perf

- **codeintel**: stat before hashing, and drop file text from the index of a large tree
- **gate**: keep the build gate from relinking the binary every run

## v0.15.2 (2026-08-27)

### Fix

- **ci**: skip the macOS-only verify-released hk step on the ubuntu hooks job

## v0.15.1 (2026-08-27)

### Fix

- **config**: allowlist edit.Replace and edit.ReplaceAll as deadcode orphans

## v0.15.0 (2026-08-27)

### Feat

- **tui**: make the thread list scroll, sort, and say what it is showing
- **cli**: repeat one recorded tool call and print what the harness answers now
- **gate**: record how long a gate waited for its resources
- **edit**: format inside the edit call and answer a stale anchor with the text
- **edit**: let str_replace change every occurrence and see past the formatter
- **tools**: name the closest indexed names when a literal search misses
- **thread**: keep the transcript so a bounded run can be picked back up
- **replay**: record what a run spent and report cost per completed task
- **agent**: give the deep tier a model the tier below it is not
- **llm**: serve the fast tier here while the machine has room and elsewhere while it does not
- **llm**: route each tier through its backend's dialect and deny data collection
- **config**: let a tier turn a hybrid model's reasoning off
- **replay**: record which model served each tier so a tier's move is visible
- **bench**: add a task whose retrieval spans two packages
- **edit**: match an anchor that dropped the file's blank lines
- **bench**: add a task that needs a failing test first, and record what undo did not settle
- **tools**: let a run put back a file it edited instead of reaching for the shell
- **tools**: tell a str_replace that broke the file's syntax from one that did not
- **gofix**: add the t.Parallel a changed test is missing instead of reporting it
- **guard**: decide from an allowlist so an unknown command asks instead of running
- wire the full-run cadence, the checks helper, and the model list
- **tools**: read version control through a tool with no verb that writes
- **guard**: refuse find, truncate, and a force push rather than approving them
- **bench**: add two replay tasks the fast tier fails without emitting a malformed call
- **deadcode**: freeze the unreached functions so the check fails on a new one
- **agent**: escalate a tier that cannot move a gate failure instead of waiting for the deadline
- **bench**: read the corpus's turns, gates, and finish checks, scoped to one harness
- **tools**: write a whole declaration by name, sending its source once
- **tools**: let one str_replace call span the files a change actually touches
- **finish**: fail a run whose whole diff is comments
- **gate**: report the linter findings the fix pass cannot fix, and stop asking the prompt
- **cli**: split the preamble's teaching prose from the grammar it hangs on
- **daemon**: queue a prompt sent mid-turn and let it interrupt the run
- **tui**: undo back to one edit instead of only the whole run
- **agent**: run the deterministic finish checks when a run completes
- **finish**: name the changed lines no test executes
- **finish**: bound a run's change set by what its task and goal name
- **finish**: fail a run whose closing answer names a path or symbol that does not exist
- **bench**: attribute each turn to productive, retrieval, or the harness
- **replay**: report the rates across every recorded run
- **app**: record a gate that retracts a failure over an unchanged change set
- **transcript**: replay a frozen run's turns so a harness change is verifiable without a model
- **agent**: record whether the gates passed on a run that stopped on a bound
- **agent**: name a malformed call and a refused repeat as their own causes
- **tools**: classify why a tool call failed so a refusal is not a defect
- **web**: add search and fetch with the defenses that do not depend on the model
- **tui**: show the turn in flight against what this run's turns cost
- **gate**: report what a failure printed when no frame names a changed file
- **tools**: add a move Modifier for relocating declarations
- **tui**: show a thread's goal in the header and behind g
- **thread**: carry a standing goal and restate it where it is lost
- **agent**: checkpoint every edit and answer what the run changed
- **shell**: answer a re-run of the project's checks from the gate log
- **search**: add a literal mode for exact substrings
- **tools**: scope a search to a path and say that OR works
- **tools**: delete several declarations in one call
- **tools**: delete a declaration by name
- **tools**: rename a symbol through the language server
- **cli**: account for the fixed preamble by section
- **tools**: reduce shell output to what names a failure
- **replay**: report the tier that actually ran each turn
- **bench**: let a replay check run the compiler
- **bench**: give each replay task an oracle and check it
- **bench**: replay a fixed task and record what the run spent
- **bench**: count the tool calls that returned an error
- **bench**: report the shell commands a run ran
- **bench**: diff two runs with -stats-vs
- **tools**: add a list tool for what the tree holds
- **bench**: add a JSON mode to -stats
- **bench**: report what a finished run spent with -stats
- **gate**: report weak tests as advisories instead of failing a run
- **router**: route turns across fast, balanced, and deep tiers

### Fix

- point copier at the published template
- **daemon**: stop reopening a thread's transcript sidecar as a thread
- **tools**: read a boolean a caller quoted as the boolean it names
- **tools**: give an ambiguous symbol refusal the path argument that resolves it
- **tools**: withhold the rename advice once the declaration is already renamed
- **codeintel**: rank fuzzy hits by the name they match, not by document length
- **tools**: point an ambiguous one-name edit at rename
- **tools**: suggest only names that hold the query as a whole word
- **agent**: record a recovered tool call on the turn that made it
- **tools**: say when an edit has already been made
- **tools**: recover the tool arguments XML mangling left intact
- **agent**: count what the gates handed the run, and stop counting a refusal against it
- **app**: wait for the index to stop before Close returns
- **tools**: answer a malformed call with the tool and the shape it needs
- **cli**: name the thread a failed run left behind
- **edit**: never restore an HTML entity whose rune is ASCII
- **gate**: run the gate that rewrites the worktree before the gates that read it
- **app**: offer the question tool only where something can answer it
- **cli**: drop the question tool where nothing can answer it
- **replay**: keep a run's transcript beside the log it kept
- **tools**: name a declare change the way the rest of the project does
- **gate**: stop failing a run for what the gate's own command got wrong
- **agent**: make a run's bounds mean what they say
- **vcs**: serialize jj against one repo so parallel lanes stop losing edits
- **edit**: report a near match where the anchor starts, not where it scores best
- **tools**: declare the per-edit path the batch shape already honors
- **agent**: log the provider failure that moves a run up a tier
- **replay**: stop two lanes started together naming the same workspace
- **replay**: keep the tree of a run that passed its checks but stopped early
- **preamble**: report the fast prefix against the window the project serves
- **llm**: send the reasoning toggle in OpenRouter's spelling too
- **app**: give a hosted fast tier the key every other hosted tier resolves
- **tools**: stop a no-op replacement telling a run its work may be done
- **edit**: name a blank source line in a near-match report instead of printing nothing
- **tools**: say which of the two mistakes a no-op replacement made
- **tools**: name the cause of every failure undo can return
- **agent**: log a stuck gate on the top tier instead of returning in silence
- **gate**: keep a stuck gate's count across a debounced re-run instead of clearing it
- **lint**: disable the gocritic check that contradicts nonamedreturns
- **gate**: stop reporting a compile error the build gate already named
- **sandbox**: keep a command from reading the secrets its own output would carry away
- **gate**: make a change path relative before it becomes a go test pattern
- **tool**: name the cause of every failure a tool can return
- **app**: stop offering the question tool where nothing can answer it
- **replay**: expire the workspaces a failing run keeps instead of stacking them
- **agent**: read back a hosted model's own tool-call dialect instead of refusing the turn
- **llm**: read a reasoning model's trace and name a turn that returned nothing
- **tools**: merge a declaration's imports instead of appending a second block
- **tools**: say when an anchor missed because the run edited the file since reading it
- **edit**: resolve every anchor in a batch against the file as read
- **app**: repeat what the gates found when a run asks the shell to re-check
- **tools**: drop a no-op edit instead of failing the batch it rides in
- **tools**: retry a wordy literal search as fuzzy instead of answering nothing
- **agent**: bound repetition on fast turns so a tool argument cannot run away
- **tools**: say a batch applies to one file so a cross-file anchor is actionable
- **thread**: keep a failed tool call's arguments whole so the failure can be read
- **tools**: require the replacement pair so a cut-short edit cannot delete
- **gate**: spell a fallback package as a directory pattern
- **tools**: write each file once per move
- **app**: review a diff on the balanced tier, not the fast one
- **tools**: name the declaration holding each blocking use
- **tools**: say how to delete a used declaration and its users together
- **tools**: refuse to delete a declaration something still uses
- **tools**: judge a widened lookup against the query it ran
- **tools**: widen a symbol lookup until a plausible name comes back
- **tools**: widen a symbol lookup that finds nothing
- **tools**: let rename take a package directory as its path
- **sched**: hold a local slot per turn and hand it over in order
- **sched**: bound local turns by the slots the server actually serves
- **vcs**: recover from a stale working copy instead of surfacing it
- **tools**: report a missing path as missing, not as outside the root
- **bench**: diff against the last replay run that took a turn
- **bench**: keep a replay workspace short enough for a unix socket
- **agent**: let a malformed tool call be sent again instead of ending the run
- **tui**: fit the too-small message in the terminal it names
- **tui**: make the model screen's confirmation answerable only once it asks
- **cli**: name the tiers -model actually takes
- **agent**: nudge any run that can edit and has not
- **agent**: tell a run that has changed nothing to start
- **guard**: refuse git writes where jj owns the working copy
- **guard**: hold git commands that move the working copy
- **llm**: accept a numeric error code in a provider's stream
- **agent**: compact against the window of the tier serving the turn
- **sandbox**: let a sandboxed build read the machine's module cache

### Refactor

- **tools**: name a wrong argument shape in JSON terms rather than Go ones
- **tools**: drop vcs, which the shell was already answering past
- **search**: stop advertising the modes that only return an error

### Perf

- **agent**: show the fast tier its own tool surface
- **config**: leave the web tools off unless a project asks for them
- **tools**: stop paying every turn for prose the failure already carries
- **app**: stop handing the model a CI runbook written for a human
- **agent**: dedupe repeated tool results when the request is assembled
- **replay**: seed a workspace from the project's derived state
- **edit**: diagnose an anchor that matches no line at all
- **edit**: name the line a failed anchor got wrong
- **tools**: apply several replacements in one str_replace call
- **gate**: tell the run which gates passed on its change
- **tools**: let read and list take several targets in one call
- **tools**: number the lines read returns
- **retrieval**: report the matching lines of a file-level search hit
- **tools**: always return what a read asked for
- **retrieval**: stop re-serving lines and OR fuzzy search terms

## v0.14.0 (2026-08-19)

### Feat

- **runtime**: serve the configured context window, route with reply room, and let a missing ast-grep abstain

### Fix

- **agent**: treat a task worded as an edit as edit-shaped and catch a retry promised after an apology

## v0.13.0 (2026-08-19)

### Feat

- **daemon**: reopen the thread logs on disk when a project loads, so Home survives a daemon restart
- **tui**: footer toast on needs-input and idle transitions, and identifiers linked from a per-repo and per-laptop pattern table
- **daemon**: park a thread blocked on input so admission goes to one that can work, and show what it parked in the inbox
- **tui**: fleet Home over the per-laptop daemon, with w toggling scope between the launch root and every root
- **tui**: tab-complete saved snippets in the fullscreen composer, stored per repo and per laptop
- **daemon**: one daemon per laptop, loading projects lazily per root over a user-level socket

### Fix

- **daemon**: fail a thread read when sync cannot catch up instead of answering from a stale cache
- **agent**: never report complete when an edit tool ran and no file changed

## v0.12.0 (2026-08-18)

### Feat

- **tui**: browse a thread's history by kind, with fuzzy matching and a grouped summary
- **tui**: give the transcript a row cursor, a fold toggle, and readable answers
- **agent**: type each turn's prose as an answer or a note from its shape

### Fix

- **daemon**: send the subscribe ack before starting its event forwarder
- **daemon**: fold a thread's log into its cache before answering, so no reply lags the stream

## v0.11.1 (2026-08-18)

### Fix

- **cli**: attach the lease holder on the headless and cycle paths so writes are not refused

## v0.11.0 (2026-08-18)

### Feat

- **diag**: parse llama-server timings and grow the diagnostics protocol
- **cycle**: advance a phase on a condition the harness evaluates
- **tui**: add a routines panel with triggers, history, and a duration sparkline
- **api**: list and run routines over the daemon socket
- **routine**: compile pkl routines into a DAG the runner can execute
- **tui**: schedule view behind s with lanes, lock waits, and the lease list
- **lease**: take a directory-subtree lease where a thread writes
- **config**: point the local tier at a llama-server on another machine
- **guard**: read the script a command runs, and let write make one executable

### Fix

- **gate**: abstain from go test on a change set with no Go file, and never fail silently
- **tui**: read the model footprint, subscribe on peek, and keep esc inside the model screen
- **daemon**: put a run that fails before its first turn on the thread log
- **guard**: expand a destructive target before judging where it points

### Refactor

- settle the lint findings from merging the M2 lanes

## v0.10.0 (2026-08-18)

### Feat

- **tui**: make the message composer modal with a fullscreen mode
- **gate**: build the coverage map so test selection reaches line level
- **cli**: report functions no main reaches

### Fix

- **typos**: unblock the repo-wide spell check

## v0.9.0 (2026-08-17)

### Feat

- **tui**: switch a thread's tier and reasoning trace mid-thread
- **gate**: run gates on change events instead of only at the end of a run
- **gate**: check LSP diagnostics on every change set
- **mention**: resolve @file and @symbol before a prompt reaches the model
- **hook**: run external pre and post tool-use commands around every tool call
- **agent**: add plan mode as a thread whose registry holds no editing tool

### Fix

- **tui**: drive every input from the theme so NO_COLOR reaches them too

## v0.8.1 (2026-08-17)

### Fix

- **agent**: bound the stream by the run deadline and stop retrying a pinned tier

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
