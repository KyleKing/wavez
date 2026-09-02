package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// `./setup.sh` says nothing about what happens next, so a guard that read
// only the command line would let a run write anything into a file and then
// execute it. The contents are what gets judged.
func assertShellOutcome(t *testing.T, wantAsked, asked, wantRefused, wantRun, isErr bool, content string) {
	t.Helper()

	if asked != wantAsked {
		t.Errorf("gate asked = %v, want %v", asked, wantAsked)
	}

	if wantRefused && !isErr {
		t.Errorf("a command that should not run returned: %s", content)
	}

	if wantRun && isErr {
		t.Errorf("an allowed command did not run: %s", content)
	}
}

func runShellCommand(
	t *testing.T, root, command string, allow []string, gate permission.GateFunc,
) tool.Result {
	t.Helper()

	in, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}

	var opts []tools.Option
	if len(allow) > 0 {
		opts = append(opts, tools.WithAllowedCommands(allow))
	}

	res, err := tools.NewShell(root, t.TempDir(), "t1", gate, opts...).Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return res
}

func TestShellJudgesTheScriptItRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		file        string
		script      string
		command     string
		wantReason  string
		allow       []string
		wantAsked   bool
		wantRun     bool
		wantRefused bool
	}{
		{
			name:    "an innocent script runs with no prompt",
			script:  "#!/bin/sh\necho hello\n",
			command: "./s.sh",
			wantRun: true,
		},
		{
			name:        "an approval-worthy line inside is caught",
			script:      "#!/bin/sh\necho hello\nrm -rf $TARGET\n",
			command:     "./s.sh",
			wantAsked:   true,
			wantRefused: true,
		},
		{
			name:        "and through an interpreter too",
			script:      "#!/bin/sh\nrm -rf $TARGET\n",
			command:     "sh s.sh",
			wantAsked:   true,
			wantRefused: true,
		},
		{
			name:        "a refused line never even reaches the gate",
			script:      "#!/bin/sh\nrm -rf /\n",
			command:     "./s.sh",
			wantRefused: true,
		},
		{
			name:    "a command naming no file is not suspicious",
			command: "echo hi",
			wantRun: true,
		},
		{
			// Read as shell, this file's docstring is a command named after
			// its first word and its print line is an `rm -rf /` to refuse.
			// It is neither: it is Python, and nothing here parses Python.
			name:        "a script in another language is not read as shell",
			file:        "gen.py",
			script:      "\"\"\"Post-Generation Script to be run from Copier.\"\"\"\nprint(\"rm -rf /\")\n",
			command:     "python3 gen.py",
			allow:       []string{"python3"},
			wantAsked:   true,
			wantRefused: true,
			wantReason:  "is not shell",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()

			name := tt.file
			if name == "" {
				name = "s.sh"
			}

			if tt.script != "" {
				//nolint:gosec // the guard's whole job here is to judge an executable script
				if err := os.WriteFile(filepath.Join(root, name), []byte(tt.script), 0o750); err != nil {
					t.Fatal(err)
				}
			}

			gate, asked := recordingGate(t, permission.Deny)

			res := runShellCommand(t, root, tt.command, tt.allow, gate)

			assertShellOutcome(t, tt.wantAsked, *asked, tt.wantRefused, tt.wantRun, res.IsError, res.Content)

			if tt.wantReason != "" && !strings.Contains(res.Content, tt.wantReason) {
				t.Errorf("Content = %q, want it to say %q", res.Content, tt.wantReason)
			}
		})
	}
}
