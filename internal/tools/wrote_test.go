package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
	"github.com/kyleking/wavez/internal/vcs"
)

func newJJRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj binary not found on PATH, skipping")
	}

	root := t.TempDir()

	cmd := exec.CommandContext(context.Background(), "jj", "git", "init", "--colocate")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj git init --colocate: %v: %s", err, out)
	}

	return root
}

func runShell(t *testing.T, root, command string, opts ...tools.Option) tool.Result {
	t.Helper()

	in, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	sh := tools.NewShell(root, t.TempDir(), "t1", permission.AllowAll(), opts...)

	res, err := sh.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run(%q): %v", command, err)
	}
	if res.IsError {
		t.Fatalf("Run(%q) failed: %s", command, res.Content)
	}

	return res
}

func changedPaths(changes []tool.Change) []string {
	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.Path)
	}

	return paths
}

// A shell command that writes source recorded nothing, so its edits reached
// no change set, took no gate attribution, and could not be undone. The
// 2026-09-03 ruff lane wrote nine source files through a Python batch editor
// this way, across 62 shell calls that reported zero changes.
func TestShellRecordsWhatItsCommandWroteThroughAnInterpreter(t *testing.T) {
	t.Parallel()

	root := newJJRepo(t)
	tree := tools.WithTree(vcs.NewJj())

	if err := os.WriteFile(filepath.Join(root, "kept.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	// The tree is already dirty here, so a report of "everything version
	// control calls changed" would name kept.py and be wrong.
	res := runShell(t, root, `python3 -c "open('edited.py','w').write('y = 2\n')"`, tree)

	if got := changedPaths(res.Changes); !slices.Equal(got, []string{"edited.py"}) {
		t.Errorf("Changes = %v, want only the file the command wrote", got)
	}
}

func TestShellRecordsAFileItsCommandWroteAgain(t *testing.T) {
	t.Parallel()

	root := newJJRepo(t)
	tree := tools.WithTree(vcs.NewJj())

	runShell(t, root, `printf 'a\n' > note.txt`, tree)

	// note.txt is dirty before this command, so only its content moving
	// tells the second write from the first.
	res := runShell(t, root, `printf 'a\nb\n' > note.txt`, tree)

	if got := changedPaths(res.Changes); !slices.Equal(got, []string{"note.txt"}) {
		t.Errorf("Changes = %v, want the rewritten file", got)
	}

	// Same length, so size alone cannot tell the two apart and the
	// modification time is what carries it.
	res = runShell(t, root, `printf 'c\nd\n' > note.txt`, tree)

	if got := changedPaths(res.Changes); !slices.Equal(got, []string{"note.txt"}) {
		t.Errorf("Changes = %v, want a same-length rewrite reported", got)
	}
}

func TestShellRecordsNothingForACommandThatOnlyReads(t *testing.T) {
	t.Parallel()

	root := newJJRepo(t)

	if err := os.WriteFile(filepath.Join(root, "kept.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	res := runShell(t, root, "ls", tools.WithTree(vcs.NewJj()))

	if len(res.Changes) != 0 {
		t.Errorf("Changes = %v, want none for a command that wrote nothing", changedPaths(res.Changes))
	}
}
