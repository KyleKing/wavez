package discover_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/what-did-ai-do/internal/discover"
)

const claudeSessionFixture = `{"type":"user","message":{"role":"user","content":"hi"},` +
	`"timestamp":"2026-01-01T10:00:00.000Z","cwd":"/repo","sessionId":"cc-1"}
`

const aiderHistoryFixture = `# aider chat started at 2026-01-02 09:00:00

#### fix the bug

internal/foo.go
` + "```" + `
<<<<<<< SEARCH
a
=======
b
>>>>>>> REPLACE
` + "```" + `

Fixed it by returning early on the error case.
`

func TestAll_CombinesAndSortsAcrossAdapters(t *testing.T) {
	home := t.TempDir()
	projDir := filepath.Join(home, ".claude", "projects", "-repo")

	if err := os.MkdirAll(projDir, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(projDir, "cc-1.jsonl"),
		[]byte(claudeSessionFixture),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cwd := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(cwd, ".aider.chat.history.md"), []byte(aiderHistoryFixture), 0o600,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("HOME", home)

	got, err := discover.All(cwd)
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	// Aider's fixture session (2026-01-02) is more recent than the Claude
	// Code fixture (2026-01-01), so it should sort first.
	if got[0].ID == "cc-1" {
		t.Errorf("got[0].ID = %q, want the more recent aider session first", got[0].ID)
	}
}

func TestAll_NoSessionsFound(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	t.Setenv("HOME", home)

	got, err := discover.All(cwd)
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}

	if len(got) != 0 {
		t.Errorf("All() = %v, want empty", got)
	}
}
