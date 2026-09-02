package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// A pipe is not a terminal, so a program that asks whether it has one is the
// cheapest proof that this gives it one. The screen is what the emulator
// resolved from the byte stream, not the stream: a program that repaints by
// moving the cursor makes those two different.
func TestPTY_RunsAProgramOnARealTerminal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := "if [ -t 0 ]; then echo HAS_TTY; else echo NO_TTY; fi; printf 'aaa\\nbbb\\n\\033[2;1HX'"
	//nolint:gosec // a fixture this test runs itself
	if err := os.WriteFile(filepath.Join(root, "probe.sh"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	gate, _ := recordingGate(t, permission.Allow)
	p := tools.NewPTY(root, t.TempDir(), "t", gate)

	res, err := p.Run(t.Context(), mustJSON(t, map[string]any{"command": "sh probe.sh"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("Result = %+v, want the screen", res)
	}

	if !strings.Contains(res.Content, "HAS_TTY") {
		t.Errorf("the program did not see a terminal:\n%s", res.Content)
	}

	// The cursor moved to the second row and wrote over what was there, so
	// the screen shows "Xaa" where the stream alone would show "aaa".
	if !strings.Contains(res.Content, "Xaa") || strings.Contains(res.Content, "aaa") {
		t.Errorf("the screen was not resolved from the stream:\n%s", res.Content)
	}
}

// A key reaches the program and what it draws afterwards comes back.
func TestPTY_WaitsForTheProgramsAnswer(t *testing.T) {
	t.Parallel()

	// A terminal echoes a keystroke the moment it is written, so the screen
	// is already still when the wait after that key begins. The slow case is
	// the flake that caused, made deterministic: a wait that ends on the echo
	// kills the program before it answers. Measured under a pty the echo
	// lands at 0 ms, and two settle windows carry such a wait to about
	// 525 ms, so this answer lands past that.
	tests := map[string]string{
		"an immediate answer":                     `sh -c 'read -r name; echo "hello $name"'`,
		"an answer after the echo has gone quiet": `sh -c 'read -r name; sleep 0.9; echo "hello $name"'`,
	}

	for name, command := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate, _ := recordingGate(t, permission.Allow)
			p := tools.NewPTY(t.TempDir(), t.TempDir(), "t", gate)

			res, err := p.Run(t.Context(), mustJSON(t, map[string]any{
				"command": command,
				"keys":    []string{"wavez\r"},
			}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !strings.Contains(res.Content, "hello wavez") {
				t.Errorf("the program's answer never reached the screen:\n%s", res.Content)
			}
		})
	}
}

// A key a program ignores draws nothing beyond its echo, and the wait for an
// answer that is not coming must not cost the whole draw bound. The program
// here reads a key, prints nothing, and holds the terminal open, so the only
// thing that can end the call is that bound.
func TestPTY_DoesNotWaitTheDrawBoundForAKeyThatDrawsNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	gate, _ := recordingGate(t, permission.Allow)
	p := tools.NewPTY(root, t.TempDir(), "t", gate)

	start := time.Now()

	if _, err := p.Run(t.Context(), mustJSON(t, map[string]any{
		"command": `sh -c 'read -r ignored; sleep 30'`,
		"keys":    []string{"x\r"},
	})); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Two waits follow the last key and each honors the answer window, so
	// the window being measured from the write rather than from the wait is
	// what keeps this at the 2.0s measured here instead of twice that.
	if elapsed := time.Since(start); elapsed > drawBoundSpent {
		t.Errorf("a key that drew nothing took %v, which is the draw bound rather than the answer window", elapsed)
	}
}

// drawBoundSpent is above every path this test can legitimately take and
// below the draw bound, so only a wait that spent that bound trips it.
const drawBoundSpent = 8 * time.Second

func TestPTY_RefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := map[string]struct {
		input map[string]any
		want  string
		cause tool.Cause
	}{
		"no command": {
			input: map[string]any{"keys": []string{"q"}},
			want:  "command is required",
			cause: tool.CauseBadInput,
		},
		"a command the guard refuses": {
			input: map[string]any{"command": "rm -rf /"},
			want:  "refused",
			cause: tool.CauseRefused,
		},
		"a screen no terminal has": {
			input: map[string]any{"command": "echo hi", "cols": 4000},
			want:  "outside 20-240",
			cause: tool.CauseBadInput,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate, _ := recordingGate(t, permission.Allow)
			res, err := tools.NewPTY(root, t.TempDir(), "t", gate).Run(t.Context(), mustJSON(t, tt.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !res.IsError || res.Cause != tt.cause {
				t.Fatalf("Result = %+v, want an error with cause %s", res, tt.cause)
			}

			if !strings.Contains(res.Content, tt.want) {
				t.Errorf("Content = %q, want it to mention %q", res.Content, tt.want)
			}
		})
	}
}

// A program that spends seconds before it prints must not be killed for
// being quiet: `go run` compiles first, and a fixed wait before the first
// read returned a blank screen and reported the tool broken.
func TestPTY_WaitsForAProgramThatStartsSlowly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	gate, _ := recordingGate(t, permission.Allow)

	res, err := tools.NewPTY(root, t.TempDir(), "t", gate).Run(t.Context(), mustJSON(t, map[string]any{
		"command": "sleep 1; echo LATE",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(res.Content, "LATE") {
		t.Errorf("gave up before the program printed:\n%q", res.Content)
	}
}

// The size the call asks for is the size the program is drawn at, which is
// what a layout written for a wider terminal has to be checked against: the
// same program wraps at 80 and does not at 120.
func TestPTY_DrawsAtTheSizeTheCallAsksFor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := map[string]struct {
		input   map[string]any
		wantRow string
	}{
		"the default 80 columns wraps it": {
			input:   map[string]any{"command": "printf '%0.s-' $(seq 1 100)"},
			wantRow: strings.Repeat("-", 80),
		},
		"120 columns does not": {
			input:   map[string]any{"command": "printf '%0.s-' $(seq 1 100)", "cols": 120},
			wantRow: strings.Repeat("-", 100),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate, _ := recordingGate(t, permission.Allow)

			res, err := tools.NewPTY(root, t.TempDir(), "t", gate).Run(t.Context(), mustJSON(t, tt.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if res.IsError {
				t.Fatalf("Result = %+v, want the screen", res)
			}

			first, _, _ := strings.Cut(res.Content, "\n")
			if first != tt.wantRow {
				t.Errorf("first row is %d columns of %q, want %d", len(first), first, len(tt.wantRow))
			}
		})
	}
}

// A project's own toolchain starts its TUI, and the shell and the terminal
// have to judge that command the same way: the allowlist reaching only one of
// them means every drive of a Python app costs an approval the shell would
// not have asked for.
func TestPTY_HonorsTheProjectAllowlist(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for name, allowed := range map[string]bool{"unlisted": false, "allowed": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate, consulted := recordingGate(t, permission.Allow)

			var opts []tools.Option
			if allowed {
				opts = append(opts, tools.WithAllowedCommands([]string{"uv"}))
			}

			res, err := tools.NewPTY(root, t.TempDir(), "t", gate, opts...).
				Run(t.Context(), mustJSON(t, map[string]any{"command": "uv --version"}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if res.IsError {
				t.Fatalf("Result = %+v, want the screen", res)
			}

			if *consulted == allowed {
				t.Errorf("gate consulted = %v for an allowlist that %s the command", *consulted, name)
			}
		})
	}
}

// A terminal that never answers a query is a terminal that hangs the program
// asking. The emulator writes its answers into a pipe, and with nothing
// reading it the parse blocks mid-screen and the whole call deadlocks: a
// Textual app asks about its modes before it draws anything, and every drive
// of one sat until the harness killed it.
func TestPTY_AnswersATerminalQuery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// DECRQM for mode 2026, then a line of output. The emulator answers the
	// query, and the program's own draw only arrives if that answer did not
	// block the reader.
	script := "printf '\\033[?2026$p'; sleep 0.2; printf 'DREW\\n'"
	//nolint:gosec // a fixture this test runs itself
	if err := os.WriteFile(filepath.Join(root, "query.sh"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	gate, _ := recordingGate(t, permission.Allow)

	done := make(chan tool.Result, 1)

	go func() {
		input := mustJSON(t, map[string]any{"command": "sh query.sh"})

		res, err := tools.NewPTY(root, t.TempDir(), "t", gate).Run(t.Context(), input)
		if err != nil {
			t.Errorf("Run: %v", err)
		}

		done <- res
	}()

	select {
	case res := <-done:
		if !strings.Contains(res.Content, "DREW") {
			t.Errorf("the program never drew past its query:\n%s", res.Content)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the call did not return: the terminal never answered the query")
	}
}

// A program at a terminal writes wherever the process can unless something
// stops it, and having a terminal is no reason to widen what a tool may
// touch: the shell runs under the same profile.
func TestPTY_WritesOnlyInsideTheProjectRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escaped")

	gate, _ := recordingGate(t, permission.Allow)
	input := mustJSON(t, map[string]any{
		"command": "echo in > inside; echo out > '" + outside + "'; echo done",
	})

	res, err := tools.NewPTY(root, t.TempDir(), "t", gate).Run(t.Context(), input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "inside")); err != nil {
		t.Errorf("a write inside the project root was refused: %v\n%s", err, res.Content)
	}

	if _, err := os.Stat(outside); err == nil {
		t.Errorf("a write outside the project root landed at %s", outside)
	}
}
