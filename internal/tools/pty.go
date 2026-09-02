package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/kyleking/wavez/internal/guard"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
)

// Bounds on one pty call. The screen is the default terminal a TUI is
// written for, the settle is what a program gets to repaint after each key
// before the next one is sent, and the answer wait is how long a key that
// draws nothing beyond its echo holds the call.
const (
	ptyCols       = 80
	ptyRows       = 24
	ptyMinCols    = 20
	ptyMinRows    = 5
	ptyMaxCols    = 240
	ptyMaxRows    = 100
	ptySettle     = 250 * time.Millisecond
	ptyAnswerWait = 2 * time.Second
	ptyKeyGap     = 2 * time.Second
	ptyDrawWait   = 15 * time.Second
	ptyPoll       = 25 * time.Millisecond
	ptyDrain      = 2 * time.Second
	ptyMaxKeys    = 40
	ptyMaxRunTime = 30 * time.Second
)

var ptySchema = buildSchema(map[string]schemaProperty{
	"command": {
		Type:        schemaTypeString,
		Description: "The program to run under the terminal, as a shell command line.",
	},
	"keys": {
		Type:  schemaTypeArray,
		Items: &schemaItems{Type: schemaTypeString},
		Description: "Keystrokes to send in order, each after the screen has settled. " +
			"Send \\r for Enter, \\t for Tab, \\u001b for Escape, and \\u0003 for Ctrl-C. " +
			"An empty list just starts the program and reads the first screen.",
	},
	"cols": {
		Type:        schemaTypeInteger,
		Description: "Terminal width, 80 by default. Change it to see the program's wrapping and truncation.",
	},
	"rows": {
		Type:        schemaTypeInteger,
		Description: "Terminal height, 24 by default.",
	},
}, "command")

// PTY runs a program under a pseudo-terminal, sends it keystrokes, and
// returns what the screen shows afterwards.
//
// It exists because a pipe is not a terminal: a TUI detects the difference
// and never renders, so the only way to see what a program draws is to give
// it one. The alternative was tmux through the shell, which the sandbox
// refuses: Seatbelt counts unix sockets in the network family and offers no
// way to scope one by path, so allowing tmux its socket would have allowed
// connecting to every socket on the machine.
//
// The program is killed when the call returns. There is no session that
// outlives it, because a terminal left running is a process the harness does
// not track, and nothing here needs one: a call sends its keys and reads the
// screen they produced.
type PTY struct {
	gate     permission.Gate
	root     string
	threadID string
	env      guard.Env
}

// NewPTY builds a PTY tool rooted at root, asking gate before it runs
// anything, since the command it is handed is as arbitrary as the shell's.
func NewPTY(root, threadID string, gate permission.Gate) *PTY {
	return &PTY{root: root, threadID: threadID, gate: gate, env: guard.Env{ProjectRoot: root}}
}

// Name implements tool.Tool.
func (*PTY) Name() string { return "pty" }

// Description implements tool.Tool.
func (*PTY) Description() string {
	return "Run a program under a real terminal, send it keystrokes, and read the screen it " +
		"drew. Use it for anything that renders rather than prints: a TUI, a full-screen " +
		"editor, an interactive prompt. What comes back is one screen, 80x24 unless cols and " +
		"rows say otherwise, so output longer than that has scrolled off as it would on a " +
		"terminal; use shell for a " +
		"program whose whole output you need. The program is stopped when the call returns, so " +
		"send every key the check needs in one call."
}

// Schema implements tool.Tool.
func (*PTY) Schema() json.RawMessage { return ptySchema }

// Risk implements tool.Tool. Exec rather than write_local: the command line
// decides what a call touches, and the guard judges that line per call.
func (*PTY) Risk() tool.RiskClass { return tool.RiskExec }

type ptyInput struct {
	Command string   `json:"command"`
	Keys    []string `json:"keys"`
	Cols    int      `json:"cols"`
	Rows    int      `json:"rows"`
}

// size is the terminal the call asks for, defaulted and bounded. A program
// drawn at a size no terminal has is a screen nobody will see, and an
// emulator sized from an unchecked number is a call that allocates whatever
// it was handed.
func (in ptyInput) size() (int, int, error) {
	cols, rows := in.Cols, in.Rows
	if cols == 0 {
		cols = ptyCols
	}

	if rows == 0 {
		rows = ptyRows
	}

	if cols < ptyMinCols || cols > ptyMaxCols {
		return 0, 0, fmt.Errorf("%w: %d columns, outside %d-%d", errScreenSize, cols, ptyMinCols, ptyMaxCols)
	}

	if rows < ptyMinRows || rows > ptyMaxRows {
		return 0, 0, fmt.Errorf("%w: %d rows, outside %d-%d", errScreenSize, rows, ptyMinRows, ptyMaxRows)
	}

	return cols, rows, nil
}

// errScreenSize reports a terminal size outside what this tool will open.
var errScreenSize = errors.New("screen size")

// Run implements tool.Tool.
func (p *PTY) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("pty: %w", err)
	}

	var in ptyInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if strings.TrimSpace(in.Command) == "" {
		return tool.Fail(tool.CauseBadInput, "command is required"), nil
	}

	if len(in.Keys) > ptyMaxKeys {
		return tool.Fail(tool.CauseBadInput,
			"%d keystrokes in one call, at most %d", len(in.Keys), ptyMaxKeys), nil
	}

	if _, _, err := in.size(); err != nil {
		return tool.Fail(tool.CauseBadInput, "%v", err), nil
	}

	if refusal, ok := p.refuse(ctx, in.Command); ok {
		return refusal, nil
	}

	screen, err := p.drive(ctx, in)
	if err != nil {
		return tool.Fail(tool.CauseIO, "%v", err), nil
	}

	return tool.Result{Content: screen}, nil
}

// refuse asks the guard and the permission gate about the command, the same
// two questions the shell asks, because what reaches a terminal is as
// arbitrary as what reaches a shell.
func (p *PTY) refuse(ctx context.Context, command string) (tool.Result, bool) {
	verdict := guard.Classify(command, p.env)

	switch verdict.Verdict {
	case guard.Refuse:
		return tool.Fail(tool.CauseRefused, "refused: %s (%q)", verdict.Reason, verdict.Fragment), true
	case guard.NeedsApproval:
		decision, err := p.gate.Ask(ctx, permission.Request{
			ThreadID: p.threadID,
			Tool:     p.Name(),
			Action:   "run",
			Detail:   command,
			Key:      approvalKey(command),
			Reason:   verdict.Reason,
		})
		if err != nil {
			return tool.Fail(tool.CauseUpstream, "requesting approval: %v", err), true
		}

		if decision == permission.Deny {
			return tool.Fail(tool.CauseRefused, "denied: %s (%q)", verdict.Reason, verdict.Fragment), true
		}
	case guard.Allow:
	}

	return tool.Result{}, false
}

// drive starts the program on a pseudo-terminal, plays the keys into it, and
// returns the screen. The emulator is what turns the program's byte stream
// into what a person would have seen: a TUI repaints by moving the cursor,
// so the stream and the screen are different things.
func (p *PTY) drive(ctx context.Context, in ptyInput) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ptyMaxRunTime)
	defer cancel()

	cols, rows, err := in.size()
	if err != nil {
		return "", err
	}

	//nolint:gosec // the command has already been through the guard and the permission gate
	cmd := exec.CommandContext(ctx, "sh", "-c", in.Command)
	cmd.Dir = p.root

	//nolint:gosec // the size is bounded well inside a uint16 by ptyInput.size
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return "", fmt.Errorf("opening a terminal for %q: %w", in.Command, err)
	}

	screen := &ptyScreen{emulator: vt.NewSafeEmulator(cols, rows)}
	done := make(chan struct{})
	exited := make(chan struct{})

	go func() {
		defer close(done)
		_, _ = io.Copy(screen, tty) //nolint:errcheck // the copy ends when the program does
	}()

	go func() {
		defer close(exited)
		_ = cmd.Wait() //nolint:errcheck // the status says nothing the screen does not
	}()

	p.play(ctx, tty, screen, exited, in.Keys)

	drain(done, exited)

	// Killing the program is what ends the reader for one that would
	// otherwise sit at a prompt. A program that already exited is past this.
	_ = cmd.Process.Kill() //nolint:errcheck // best effort: the program may have exited already
	_ = tty.Close()        //nolint:errcheck // as above
	<-done
	<-exited

	return strings.TrimRight(screen.emulator.Render(), " \n"), nil
}

// drain gives the reader time to finish once the program has exited, before
// the terminal is closed under it. Closing the master discards whatever the
// program wrote and nothing has read yet, which is how a one-shot program's
// last line went missing under a loaded machine while passing every time on
// an idle one.
func drain(done, exited <-chan struct{}) {
	select {
	case <-exited:
	default:
		return
	}

	timer := time.NewTimer(ptyDrain)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
	}
}

// ptyScreen is the emulator with the time of its last write, which is what
// tells a caller the program has stopped drawing, and enough about the last
// keystroke to tell the program's drawing from the terminal's own.
type ptyScreen struct {
	last time.Time
	// sent is when the last keystroke was written, zero before the first.
	sent     time.Time
	emulator *vt.SafeEmulator
	// echo is what the line discipline still owes back for that keystroke.
	echo []byte
	// answer records a draw that was not that echo, which is the only
	// evidence the program itself reacted.
	answer bool
	mu     sync.Mutex
}

// expect records the keystroke just written so its echo does not read as an
// answer. A terminal in canonical mode writes the keystroke back with \r
// mapped to \r\n; one in raw mode writes nothing back, and there the next
// draw fails to match and answers as it should.
func (s *ptyScreen) expect(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.echo = []byte(strings.ReplaceAll(key, "\r", "\r\n"))
	s.answer = false
	s.sent = time.Now()
}

// note consumes whatever of b the terminal still owed as echo and reads the
// rest as the program answering. The echo can arrive split across writes or
// joined to the answer, so it is matched byte by byte rather than whole.
func (s *ptyScreen) note(b []byte) {
	n := 0
	for n < len(b) && n < len(s.echo) && b[n] == s.echo[n] {
		n++
	}

	s.echo = s.echo[n:]

	if n < len(b) {
		s.echo = nil
		s.answer = true
	}
}

// answered reports whether the program has drawn anything the terminal did
// not echo on its behalf.
func (s *ptyScreen) answered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.answer
}

// awaited reports whether the last keystroke has had its window to be
// answered. A key a program ignores draws nothing beyond its echo, and
// waiting the whole draw bound for an answer that is not coming is what
// would make every such call slow. Measured from the write rather than from
// the wait, so the two waits one key gets do not each spend the window.
//
// It is false before the first keystroke, which is what leaves a program
// that has yet to draw at all waiting its whole bound: that one may still
// be starting up.
func (s *ptyScreen) awaited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return !s.sent.IsZero() && time.Since(s.sent) >= ptyAnswerWait
}

func (s *ptyScreen) Write(b []byte) (int, error) {
	s.mu.Lock()
	s.last = time.Now()
	s.note(b)
	s.mu.Unlock()

	n, err := s.emulator.Write(b)
	if err != nil {
		return n, fmt.Errorf("writing to the screen: %w", err)
	}

	return n, nil
}

// quiet reports whether nothing has been drawn for settle, which is what
// says a program that answered has finished answering: it may exit, or it
// may sit at a prompt, and both look the same from outside.
//
// A program that has drawn nothing is never quiet, so a caller waits its
// whole bound for one: the alternative treated a program still compiling as
// one that had finished, and killed it before it printed.
func (s *ptyScreen) quiet(settle time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return !s.last.IsZero() && time.Since(s.last) > settle
}

// play sends each keystroke and lets the screen settle between them, since a
// key sent into a program still repainting from the last one is a key the
// program may never see.
//
// Nothing waits before the first key. A terminal buffers what is written to
// it, so a program reads its input when it is ready, and waiting for a first
// paint that has not come yet cannot tell a program compiling from one
// waiting to be typed at.
func (*PTY) play(ctx context.Context, tty io.Writer, screen *ptyScreen, exited <-chan struct{}, keys []string) {
	for _, k := range keys {
		screen.expect(k)

		if _, err := io.WriteString(tty, k); err != nil {
			return
		}

		settle(ctx, screen, exited, ptyKeyGap)
	}

	settle(ctx, screen, exited, ptyDrawWait)
}

// settle waits for the screen to be drawn and then stop changing, bounded by
// bound so a program that draws forever, or never, still returns something.
//
// A program that exits is the precise signal and is taken first. Otherwise
// the program has to have answered before quiet means anything, since a
// terminal echoes a keystroke and a screen still on that echo says only that
// the terminal is done.
//
// A fixed sleep was what all of this replaced, twice. Under a loaded machine
// the program had not drawn when the sleep ended and the call returned a
// blank screen; then `go run` spent its first seconds compiling and was
// killed before it printed anything at all.
func settle(ctx context.Context, screen *ptyScreen, exited <-chan struct{}, bound time.Duration) {
	entered := time.Now()

	deadline := time.NewTimer(bound)
	defer deadline.Stop()

	tick := time.NewTicker(ptyPoll)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-exited:
			return
		case <-deadline.C:
			return
		case <-tick.C:
			// Quiet alone cannot end this wait. A terminal echoes a keystroke
			// the moment it is written, so the screen goes still on that echo
			// within the settle window while the program has not been
			// scheduled yet: measured under a pty, the echo landed at 0 ms and
			// the answer at 407 ms, and the wait was over at 275 ms.
			if !screen.answered() {
				if screen.awaited() {
					return
				}

				continue
			}

			// The quiet window has to be one this wait watched, or a screen
			// already still for longer than it ends the wait on arrival.
			if time.Since(entered) >= ptySettle && screen.quiet(ptySettle) {
				return
			}
		}
	}
}
