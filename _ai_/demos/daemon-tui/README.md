# daemon-tui spike

Derisks the v0.1 wavez architecture: a daemon holding threads, a Bubble Tea v2
client talking to it over a unix socket, newline-delimited JSON, fan-out to
subscribers. `cmd/wavezd` fakes 3 threads streaming 20-50 events/sec each.
`cmd/wavez` is the client: Home list, Thread view with a virtualized
transcript, input line, footer.

## Run it

```
go build -o /tmp/wavezd ./cmd/wavezd && /tmp/wavezd &
go build -o /tmp/wavez ./cmd/wavez && /tmp/wavez
```

Keys: `enter` open/send, `esc` back, `y`/`n` answer a pending permission,
`[`/`]` switch thread, `up`/`down` move/scroll.

## Numbers (macOS arm64, 3 threads streaming, permissions auto-answered)

- Combined event rate: ~97/sec sustained (each thread's own rate is 20-50/sec
  as specced; permission gates that go unanswered stall a thread for up to
  5s, so an unattended run is much slower, closer to 15-20/sec combined).
- Daemon at ~105k backlog events (all 3 threads, nothing evicted): 1.5% CPU
  avg, 23-33MB RSS / 28.7MB physical footprint. RSS grows in ~9MB steps, not
  smoothly, matching Go slice-doubling on the backlog `[]Event`.
  Unbounded backlog is a real memory-growth risk to solve before v0.1, not
  just a spike artifact.
- Client at the same point: 46-58MB physical footprint, holding full history
  for all 3 threads (by design, per spec: transcript keeps everything in
  memory and only renders the visible window). CPU crept from under 1% early
  in the run to 8-11% once the in-memory event log got large, on both the
  Home and Thread screens. That's GC pressure on a growing heap, not
  redraw cost. The 16ms batch window itself doesn't help this because it
  coalesces bursts, it doesn't cap total events retained.
- Input stayed responsive throughout, including while both other threads
  kept streaming and one had 30k+ backlogged events: typed text round-tripped
  through the daemon's `send` command and appeared in the transcript same
  redraw cycle a `list`/`answer` round trip returned.
- Kill/restart: daemon has no client and no crash; on reconnect the new
  client's Home counts are already current and reopening a thread replays
  the full backlog exactly (`events` count matches `seq`), confirming
  threads survive TUI restarts.

## What broke

Found and fixed two real concurrency bugs while running the load test, both
in `wavezd`, both about the fan-out from thread to per-connection channel:

1. `conn.send` used a non-blocking channel send with a `default:` drop. Under
   backlog replay (a fresh subscribe pushing thousands of buffered events
   fast) this silently dropped messages once the 4096-slot outbound buffer
   filled: a client reconnecting after ~4700 events only replayed 1984 of
   them. Fixed by making `send` block; only the live per-thread fan-out in
   `thread.emit` is allowed to shed load for a slow subscriber.
2. `thread.unsubscribe` closed the per-connection event channel while
   `thread.emit` could still be mid-send to it from another goroutine
   (`send on closed channel` panic). Fixed by never closing that channel;
   the forwarding goroutine now exits via a per-connection `stop` channel,
   and a `sync.WaitGroup` makes sure all forwarders have exited before the
   connection's outbound channel gets closed. Verified with `go build -race`
   plus 400 rapid connect/subscribe/disconnect cycles: no race, no panic.

## v1 to v2 (for Crush-era code)

Import paths move to `charm.land/...` (bubbletea, bubbles, lipgloss all
vanity-domain now). `View() string` becomes `View() tea.View`; alt screen,
mouse mode, and other things that used to be `tea.WithX()` program options
are now fields you set on the returned `tea.View`. `tea.KeyMsg` is now an
interface covering both press and release; match `tea.KeyPressMsg` and its
`.String()` the same way you matched v1's `KeyMsg`. Redraws are capped at
`WithFPS` (default 60, max 120) and diffed against a cell buffer
(`ultraviolet.ScreenBuffer`) rather than v1's string diffing, which is the
direct answer to charmbracelet/bubbletea#1724-class scroll cost: we didn't
reproduce a stall in this spike, and the renderer's own docs cite the
cell-based renderer as the fix for that class of problem.

`bubbles/v2` has both `viewport` (scrollable content, but `SetContent` wants
the whole string and its own virtualization isn't built for a live-growing
event log) and `list` (heavyweight, fuzzy-filterable, wrong shape). Rolled
our own: the transcript is a direct slice index into `[]proto.Event`, no
string join, no widget. Simplest thing that satisfies "only render visible
rows, keep everything in memory."

## Verdict

Daemon+client over a unix socket holds up for this: fan-out worked, restart
reattachment worked, input stayed responsive under 100+ events/sec across 3
threads. The two bugs found were exactly the kind a spike should surface
before v0.1: non-blocking sends need an explicit backpressure or drop
policy per path, and closing a channel a producer still writes to is a
footgun regardless of language experience. Bubble Tea v2 is solid for a
streaming transcript: the FPS cap and cell-based diffing make raw redraw
cost a non-issue, but "keep all events in memory forever" needs a
retention policy (ring buffer, on-disk overflow) before real threads run
for hours, on both sides of the socket, not just the client's transcript.
