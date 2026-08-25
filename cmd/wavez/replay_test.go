package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/vcs"
)

// A replay keeps the workspace of any run whose checks did not all pass,
// which is most of them, and nothing expired them: 78 accumulated in this
// repo and every jj command rebased their commits.
func TestPruneKeptWorkspacesLeavesTheMostRecent(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj is not installed")
	}

	ctx := context.Background()
	root := t.TempDir()
	jjCmd(t, root, "git", "init", "--colocate")

	jj := vcs.NewJj()

	const created = keptReplayWorkspaces + 3

	names := make([]string, 0, created)

	// The names land in the same scratch directory a real replay uses, so
	// they carry this process's id to keep two runs from colliding there.
	base := int64(os.Getpid()) * created

	if replayWorkspaceName(time.Unix(0, base), 1) == replayWorkspaceName(time.Unix(0, base), 2) {
		t.Fatal("two lanes starting on the same nanosecond must not name the same workspace")
	}

	for i := range created {
		name := replayWorkspaceName(time.Unix(0, base+int64(i)), os.Getpid())
		dir := filepath.Join(scratchBase(), name)
		t.Cleanup(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("removing %s: %v", dir, err)
			}
		})

		if err := jj.AddWorkspace(ctx, root, name, dir); err != nil {
			t.Fatalf("AddWorkspace %s: %v", name, err)
		}

		names = append(names, name)
	}

	pruneKeptWorkspaces(ctx, jj, root)

	left, err := jj.Workspaces(ctx, root)
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}

	survivors := map[string]bool{}
	for _, n := range left {
		survivors[n] = true
	}

	for i, name := range names {
		want := i >= created-keptReplayWorkspaces
		if survivors[name] != want {
			t.Errorf("workspace %s survived = %v, want %v", name, survivors[name], want)
		}

		if _, err := os.Stat(filepath.Join(scratchBase(), name)); (err == nil) != want {
			t.Errorf("directory for %s exists = %v, want %v", name, err == nil, want)
		}
	}
}

func jjCmd(t *testing.T, dir string, args ...string) {
	t.Helper()

	//nolint:gosec // this test's own fixed fixture-setup commands
	cmd := exec.CommandContext(context.Background(), "jj", args...)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj %v: %v: %s", args, err, out)
	}
}
