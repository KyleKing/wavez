package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/stakes"
	"github.com/kyleking/wavez/internal/tools"
)

// TestChangeSet_EditsScoreTheNextPermissionPrompt is the wiring this
// package exists to prove: a capability an edit introduced reaches the
// permission prompt for a later command, so approving that command blind
// costs the user the run's evidence rather than the command's alone.
func TestChangeSet_EditsScoreTheNextPermissionPrompt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "run.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc run() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	changes := stakes.NewChangeSet()

	var captured permission.Request

	gate := permission.GateFunc(func(_ context.Context, req permission.Request) (permission.Decision, error) {
		captured = req

		return permission.Deny, nil
	})

	if err := os.WriteFile(filepath.Join(root, "net.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Applied in order into one shared ChangeSet, so these are steps of a
	// single run rather than independent cases.
	edits := [][3]string{
		{"run.go", "func run() {}", "func run() { _ = exec.Command(\"ls\") }"},
		{"net.go", "package main", "package main\n\nimport \"net/http\""},
	}

	edit := tools.NewStrReplace(root, changes)

	for _, e := range edits {
		result, err := edit.Run(context.Background(), mustJSON(t, map[string]any{
			"path": e[0], "old_string": e[1], "new_string": e[2],
		}))
		if err != nil {
			t.Fatalf("editing %s: %v", e[0], err)
		}

		if result.IsError {
			t.Fatalf("editing %s: IsError = true, want false: %q", e[0], result.Content)
		}
	}

	sh := tools.NewShell(root, root, "thread-1", gate, changes)
	if _, err := sh.Run(context.Background(),
		mustJSON(t, map[string]any{"command": "git push --force"})); err != nil {
		t.Fatalf("shell Run: %v", err)
	}

	score := captured.Stakes
	if score == nil {
		t.Fatal("permission request carried no Stakes")
	}
	if !score.CapsChecked {
		t.Error("CapsChecked = false, want true once edits were recorded")
	}
	if score.EditedFiles != 2 {
		t.Errorf("EditedFiles = %d, want 2", score.EditedFiles)
	}
	if score.Band != stakes.BandHigh {
		t.Errorf("Band = %q, want %q once an edit introduced a capability", score.Band, stakes.BandHigh)
	}

	want := map[stakes.Capability]bool{
		stakes.CapabilitySubprocess: true,
		stakes.CapabilityNetwork:    true,
		stakes.CapabilityImport:     true,
	}
	for _, c := range score.Capabilities {
		delete(want, c)
	}

	if len(want) > 0 {
		t.Errorf("Capabilities = %v, missing %v", score.Capabilities, want)
	}
}

// TestChangeSet_NilRecorderLeavesTheSignalUnknown proves an unwired
// ChangeSet degrades to unknown rather than to a computed-empty result, so
// a caller that never recorded edits cannot read as one that recorded none.
func TestChangeSet_NilRecorderLeavesTheSignalUnknown(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	var captured permission.Request

	gate := permission.GateFunc(func(_ context.Context, req permission.Request) (permission.Decision, error) {
		captured = req

		return permission.Deny, nil
	})

	sh := tools.NewShell(root, root, "thread-1", gate, nil)
	if _, err := sh.Run(context.Background(),
		mustJSON(t, map[string]any{"command": "git push --force"})); err != nil {
		t.Fatalf("shell Run: %v", err)
	}

	if captured.Stakes == nil {
		t.Fatal("permission request carried no Stakes")
	}
	if captured.Stakes.CapsChecked {
		t.Error("CapsChecked = true with no recorder wired, want false")
	}
}
