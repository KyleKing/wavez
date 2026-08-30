package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	p := tools.NewPTY(root, "t", gate)

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
func TestPTY_SendsKeystrokesAndReadsWhatTheyDrew(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	gate, _ := recordingGate(t, permission.Allow)
	p := tools.NewPTY(root, "t", gate)

	res, err := p.Run(t.Context(), mustJSON(t, map[string]any{
		"command": `sh -c 'read -r name; echo "hello $name"'`,
		"keys":    []string{"wavez\r"},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(res.Content, "hello wavez") {
		t.Errorf("the keystroke did not reach the program:\n%s", res.Content)
	}
}

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
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate, _ := recordingGate(t, permission.Allow)
			res, err := tools.NewPTY(root, "t", gate).Run(t.Context(), mustJSON(t, tt.input))
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
