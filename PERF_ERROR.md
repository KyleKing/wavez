# Runaway vcr-tui processes: investigation notes

On 2026-09-02, a syswatch snapshot showed 19 orphaned `vcr-tui` processes
(`ppid 1`), each pinned at 40-49% CPU, running for up to ~10 hours. An old,
also-orphaned `wavezd` (dir `~/Developer/kyleking/vcr-tui`, socket
`/tmp/wz-vcr/d.sock`) was alive alongside them. All were killed by hand;
nothing here has been fixed yet. This file is the starting point for that
work, split into the four questions worth chasing.

## A. Reaping processes wavez spawns

Reaping a spawned process was supposed to be one of the design wins here,
and this incident says it isn't holding in practice. Two spawn paths exist
today and neither has a supervisor independent of the process that started
them:

- `internal/tools/pty.go`'s `drive()` kills, closes, and waits on its child
  only as the last lines of that same function (`cmd.Process.Kill()`,
  `tty.Close()`, `screen.emulator.Close()`). If the `wavezd` goroutine
  running `drive()` never gets there — the daemon crashes, is force-killed,
  the machine sleeps mid-call — the child is never touched. `ctx`'s 30s
  timeout (`ptyMaxRunTime`) only fires if that goroutine, and the process
  hosting it, is still alive to observe `ctx.Done()`.
- `AGENTS.md`'s documented manual TUI-testing flow (`tmux new -d -x 80 -y 24
  <cmd>`, drive with `send-keys`, read with `capture-pane`) has no matching
  teardown step. Nothing says to `tmux kill-session` when the check is done,
  so a manual exploratory session that spawns vcr-tui this way leaves it
  running indefinitely unless someone remembers to kill it by hand.

Open questions:

- Should `wavezd` track every PID (or pty-tool child) it spawns in a table
  independent of the goroutine that started it, and sweep that table on
  daemon startup and shutdown? A daemon crash mid-call currently leaves no
  record anywhere of what it had running.
- Should the `pty` tool put its child in its own process group
  (`Setpgid`) and kill the group rather than the single PID, so a
  `sh -c` command with shell operators (`&&`, `;`, pipes) that forks a real
  child gets cleaned up too? Today's code assumes `cmd.Process` IS the
  running program, which only holds because `sh -c` exec-replaces itself
  for a single simple command — true for the calls seen here, not
  guaranteed for every future `pty` call.
- Is `/tmp/wz-vcr/d.sock` (a stable, reused socket path outside the
  scratch-daemon convention `AGENTS.local.md` documents) itself a bug? The
  convention says a test/demo daemon passes `-socket <scratch path>`
  specifically so it doesn't collide with anything, including a later
  invocation of the same test. A fixed non-scratch socket path is exactly
  how an old, stale daemon instance goes unnoticed: a new run can't tell
  whether it's reusing a live daemon or leaving an old one to rot.
- Add the manual-testing teardown step to `AGENTS.md`'s TUI Testing
  section: kill the tmux session once a check is read back, and name the
  session so a stray one is identifiable later (`tmux kill-session -t
  wz-check` rather than an anonymous `-d`).

## B. Why vcr-tui idles at 40-49% CPU instead of near 0%

Not proven. The processes were killed before anything could trace them
(`py-spy`, `strace`/`dtrace`), so this is a list of candidates to check
next time, not a diagnosis.

vcr-tui is a Textual app (`src/vcr_tui/app.py`, `App.run()` with no custom
driver arguments). Nothing in `src/vcr_tui/` polls on a timer
(`set_interval`, `call_later`, `asyncio.sleep` were all absent from a grep
of the app code), so a busy loop, if that's what this is, is either inside
Textual/asyncio itself or in how the pty on the other end behaves, not in
vcr-tui's own application code.

Candidates, roughly in order of how well they fit the evidence:

1. **Terminal mode/capability query loop.** `internal/tools/pty.go` has a
   comment noting "a Textual app queries its modes on startup and
   deadlocked here every time" — i.e. this exact interaction (vcr-tui
   against wavez's pty emulator) has already misbehaved once. If the
   emulator's answer to a mode query itself triggers vcr-tui to ask again
   (rather than settling), that's an infinite ask-answer loop entirely in
   user space, with neither side blocking on real I/O. Weakly against this:
   the daemon side (`wavezd`, PID 31026) was measured at ~0.4% CPU, not
   matching CPU also going into answering a tight query loop — but that
   measurement came from a stale snapshot, not a live trace, so it doesn't
   rule this out.
2. **Non-interactive pty confusing Textual's driver.** Textual's Linux/Mac
   driver expects a real interactive terminal. Run under a pty that's open
   but never produces real user input (no one attached, no resize, no
   keypresses), some driver code paths may fall back to polling behavior
   instead of blocking on `select`/`epoll` with no timeout.
3. **A held-open pty with a closed/gone read side.** If the daemon's own
   process (or the goroutine holding the pty master) died without running
   the `tty.Close()` line, the slave side vcr-tui is attached to would
   still function normally (not EIO/hangup) — this doesn't fit as well as
   #2 for a *sustained* spin, since a genuinely open, quiet pty shouldn't
   cost CPU on its own.

To make idle time actually idle:

- Reproduce live: launch one `vcr-tui fixtures/cassettes` under a scratch
  `tmux` session (per `AGENTS.md`'s TUI Testing flow) and, without sending
  any keys, watch `top`/`ps` for a minute. If it idles near 0%, the bug is
  specific to the wavez `pty` tool's emulator, not vcr-tui standalone —
  that would point straight at candidate 1 or 3.
- If it reproduces standalone: attach `py-spy dump`/`py-spy top` to a
  spinning instance and read the actual hot frame. That answers B directly
  instead of guessing further.
- Once the loop is identified, the fix is almost certainly "block instead
  of poll" (an `await`/`select` with no busy-wait) rather than anything
  about vcr-tui's own feature code — Textual apps are event-driven and
  shouldn't burn CPU with no events arriving.
- Energy-efficiency angle, once B is fixed rather than as a workaround: a
  TUI has no legitimate reason to run unattended for hours. Consider
  whether `vcr-tui` should exit (or Textual's own idle-timeout, if it
  has one) after some period with no input, independent of who launched
  it — that caps the damage of the next thing in this file that forgets to
  kill it.

## C. What likely happened here, end to end

Best-supported reconstruction, not confirmed:

1. An earlier session ran a burst of TUI checks against vcr-tui —
   17 plain `vcr-tui fixtures/cassettes` runs plus 2 `vcr-tui --channel
   yaml .wavez/multi` runs — either through the `pty` tool or via the
   manual `tmux new -d` pattern in `AGENTS.md`, spread across roughly
   30 minutes (process start times clustered 12:34-1:05 AM).
2. Whatever supervised those runs (an old `wavezd`, or the session itself)
   went away without tearing them down — no `tmux kill-session`, no PID
   tracking to sweep, and (per A) the `pty` tool's cleanup code only runs
   inline in the goroutine that started each call.
3. Each orphaned vcr-tui, reparented to `init`, kept running and (per B)
   kept burning CPU instead of idling, for as long as ~10 hours until this
   session found and killed them.
4. A stale `wavezd` (PID 31026) survived the same way, on the fixed
   `/tmp/wz-vcr/d.sock` socket path, itself orphaned to `init`.

Proposed fixes, ranked by how directly they address the reconstruction
above:

- **Track and sweep spawned PIDs** (A) — turns "orphaned and invisible" into
  "orphaned and known", which is the precondition for any automated
  cleanup.
- **Fix the idle-CPU bug** (B) — even without perfect reaping, an orphaned
  process idling at ~0% is a rounding error; the same incident at 45% CPU
  × 19 processes for 10 hours is what actually got noticed.
- **Document and enforce teardown on the manual tmux flow** — cheapest fix,
  addresses the specific path that most likely produced this batch.
- **Retire the fixed scratch socket path** (`/tmp/wz-vcr/d.sock`) in favor
  of one scoped per test run, so a stale daemon can't silently persist
  under a name a new run will reuse without checking.

## D. Error handling when a spawned process runs amok

Nothing today watches a `pty`-tool child's resource use during its run, or
after wavez itself is gone. Ideas, roughly cheapest first:

- **A liveness/resource check inside `drive()`'s existing loop.** It
  already has a `settle`/`quiet` polling structure watching the screen for
  activity; the same loop could sample the child's CPU% (e.g. via
  `/proc`-equivalent or `ps -o pcpu= -p <pid>` on macOS) and abort the call
  early — separately from the 30s hard timeout — if a process is pinned
  near 100% CPU with no corresponding screen activity, which is exactly
  the signature this incident produced.
- **A standalone reaper, independent of any single `pty` call.** Something
  `wavezd` runs on an interval (or on startup, sweeping what it inherits)
  that lists its own known-spawned PIDs, checks each one's CPU/RSS/age
  against a threshold, and kills anything that's both long-running and
  hot. This is the general form of A's "track spawned PIDs" plus a policy
  for when to act on the list, and it's what would have caught this
  incident automatically rather than by manual `ps` inspection.
- **Surface it, don't just kill it.** A reaper that silently kills a
  runaway process hides a real bug (see B) behind cleanup. At minimum log
  what it killed, why (CPU/RSS/age thresholds crossed), and the command
  line, so the next investigation starts from evidence instead of another
  `ps aux | grep`.
- **Cap blast radius per call, not just per process.** `ptyMaxRunTime` caps
  wall-clock time for one `pty` call; nothing caps aggregate CPU time or
  concurrent spawned-process count across calls. A daemon that's spawned,
  say, 20 children awaiting cleanup is itself a signal something upstream
  is wrong (a caller not waiting for one call to finish before starting
  the next, or a retry loop with no backoff) worth surfacing on its own.

## Open questions to carry into the next session

- Does Textual (or its driver layer) have a documented idle-CPU issue
  running under a non-interactive pty, or is this specific to how wavez's
  `pty` tool answers terminal queries? Worth checking Textual's issue
  tracker before assuming the bug is local.
- Was this batch produced by the `pty` tool, the manual `tmux` flow, or
  something else entirely (a test harness, a `routine`/`gate` run)? The
  reconstruction in C is a best guess from timing and command shape, not
  confirmed by any log — `wavezd`'s own logs or `.wavez/threads/` history
  from that time window, if still present, might settle this directly.
- If PID tracking lands (A), does it belong in `wavezd` itself, or in a
  thin wrapper any spawner (the `pty` tool, a future one) registers with,
  so the tracking logic isn't duplicated per tool?
- What's the right default for "a process has been running unattended too
  long" — wall-clock age alone, CPU accumulated, or (better) CPU with no
  corresponding output/screen activity, which is what actually
  distinguished this incident's processes from a legitimately long test
  run?
