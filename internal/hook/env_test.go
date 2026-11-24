package hook_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/hook"
)

func TestExportSession_TheShellReadsTheIDBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
	}{
		{"a plain id", "0a1b2c3d-session"},
		{"an id with a quote", "it's"},
		{"an id with shell syntax", "$(touch pwned); `x`"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			envFile := filepath.Join(dir, "env")

			if err := os.WriteFile(envFile, []byte("export OTHER=1\n"), 0o600); err != nil {
				t.Fatalf("writing the env file: %v", err)
			}

			if err := hook.ExportSession(envFile, tt.id); err != nil {
				t.Fatalf("ExportSession: %v", err)
			}

			cmd := exec.CommandContext(t.Context(), "sh", "-c", `. ./env && printf %s "$OTHER:$WAVEZ_SESSION"`)
			cmd.Dir = dir

			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("sourcing the env file: %v", err)
			}

			if got := string(out); got != "1:"+tt.id {
				t.Errorf("the shell read %q, want %q", got, "1:"+tt.id)
			}

			if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
				t.Error("the id ran as a command")
			}
		})
	}
}
