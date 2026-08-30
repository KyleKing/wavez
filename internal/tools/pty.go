package tools

import (
	"context"
	"encoding/json"
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
// written for, and the settle is what a program gets to repaint after each
// key before the next one is sent.
const (
	ptyCols       = 80
	ptyRows       = 24
	ptySettle     = 250 * time.Millisecond
	ptySettleMax  = 5 * time.Second
	ptyPoll       = 25 * time.Millisecond
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
		"editor, an interactive prompt. The program is stopped when the call returns, so send " +
		"every key the check needs in one call."
}

// Schema implements tool.Tool.
func (*PTY) Schema() json.RawMessage { return ptySchema }

type ptyInput struct {
	Command string   `json:"command"`
	Keys    []string `json:"keys"`
}

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

	//nolint:gosec // the command has already been through the guard and the permission gate
	cmd := exec.CommandContext(ctx, "sh", "-c", in.Command)
	cmd.Dir = p.root

	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: ptyCols, Rows: ptyRows})
	if err != nil {
		return "", fmt.Errorf("opening a terminal for %q: %w", in.Command, err)
	}

	screen := &ptyScreen{emulator: vt.NewSafeEmulator(ptyCols, ptyRows), started: time.Now()}
	done := make(chan struct{})

	go func() {
		defer close(done)
		_, _ = io.Copy(screen, tty) //nolint:errcheck // the copy ends when the program does
	}()

	p.play(ctx, tty, screen, in.Keys)

	// Killing the program is what ends the reader for one that would
	// otherwise sit at a prompt. A program that already exited is past this.
	_ = cmd.Process.Kill() //nolint:errcheck // best effort: the program may have exited already
	_ = tty.Close()        //nolint:errcheck // as above
	<-done
	_ = cmd.Wait() //nolint:errcheck // the exit status of a killed program says nothing

	return strings.TrimRight(screen.emulator.Render(), " \n"), nil
}

// ptyScreen is the emulator with the time of its last write, which is what
// tells a caller the program has stopped drawing.
type ptyScreen struct {
	emulator *vt.SafeEmulator
	started  time.Time
	last     time.Time
	mu       sync.Mutex
}

func (s *ptyScreen) Write(b []byte) (int, error) {
	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()

	n, err := s.emulator.Write(b)
	if err != nil {
		return n, fmt.Errorf("writing to the screen: %w", err)
	}

	return n, nil
}

// quiet reports whether nothing has been drawn for settle, which is the only
// signal available that a program has finished responding: it may exit, or it
// may sit at a prompt, and both look the same from outside.
//
// A program that has drawn nothing at all is measured from when it started,
// because waiting for a first paint that is never coming is how a program
// reading its input before printing anything would burn the whole bound.
func (s *ptyScreen) quiet(settle time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	since := s.last
	if since.IsZero() {
		since = s.started
	}

	return time.Since(since) > settle
}

// play sends each keystroke once the screen has stopped changing, since a
// key sent into a program still repainting from the last one is a key the
// program may never see.
func (*PTY) play(ctx context.Context, tty io.Writer, screen *ptyScreen, keys []string) {
	settle(ctx, screen)

	for _, k := range keys {
		if _, err := io.WriteString(tty, k); err != nil {
			return
		}

		settle(ctx, screen)
	}
}

// settle waits for the screen to stop changing, bounded by ptySettleMax so a
// program that draws forever still returns something. A fixed sleep was what
// this replaced: under a loaded machine the program had not drawn at all when
// the sleep ended, and the call returned a blank screen.
func settle(ctx context.Context, screen *ptyScreen) {
	deadline := time.NewTimer(ptySettleMax)
	defer deadline.Stop()

	tick := time.NewTicker(ptyPoll)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
			if screen.quiet(ptySettle) {
				return
			}
		}
	}
}
