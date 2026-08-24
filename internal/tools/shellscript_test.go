package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
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

func TestShellJudgesTheScriptItRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		script      string
		command     string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if tt.script != "" {
				//nolint:gosec // the guard's whole job here is to judge an executable script
				if err := os.WriteFile(filepath.Join(root, "s.sh"), []byte(tt.script), 0o750); err != nil {
					t.Fatal(err)
				}
			}

			gate, asked := recordingGate(t, permission.Deny)

			in, err := json.Marshal(map[string]any{"command": tt.command})
			if err != nil {
				t.Fatal(err)
			}

			res, err := tools.NewShell(root, t.TempDir(), "t1", gate).Run(context.Background(), in)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			assertShellOutcome(t, tt.wantAsked, *asked, tt.wantRefused, tt.wantRun, res.IsError, res.Content)
		})
	}
}
