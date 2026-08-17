package vcs_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/vcs"
)

func requireJJ(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj binary not found on PATH, skipping")
	}
}

func newFixtureRepo(t *testing.T) string {
	t.Helper()
	requireJJ(t)

	dir := t.TempDir()
	runJJCmd(t, dir, "git", "init", "--colocate")
	writeFile(t, dir, "a.go", "package a\n")

	return dir
}

func runJJCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()

	//nolint:gosec // args are this test's own fixed fixture-setup commands
	cmd := exec.CommandContext(context.Background(), "jj", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jj %v: %v: %s", args, err, out)
	}

	return string(out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestJjRepoRoot(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	j := vcs.NewJj()

	got, err := j.RepoRoot(context.Background(), sub)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}

	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	resolvedGot, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if resolvedGot != resolvedDir {
		t.Fatalf("RepoRoot = %q, want %q", resolvedGot, resolvedDir)
	}
}

func TestJjRepoRootNotAJJRepo(t *testing.T) {
	t.Parallel()
	requireJJ(t)

	dir := t.TempDir()
	j := vcs.NewJj()

	_, err := j.RepoRoot(context.Background(), dir)
	if err == nil {
		t.Fatal("RepoRoot: expected error for a non-jj directory")
	}
	if !errors.Is(err, vcs.ErrNotJJRepo) {
		t.Fatalf("RepoRoot error = %v, want it to wrap ErrNotJJRepo", err)
	}
	if !strings.Contains(err.Error(), vcs.InitHint) {
		t.Fatalf("RepoRoot error = %q, want it to name the actionable fix %q", err.Error(), vcs.InitHint)
	}
}

func TestJjCaptureRestore(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	j := vcs.NewJj()
	ctx := context.Background()

	checkpoint, err := j.Capture(ctx, dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if checkpoint == "" {
		t.Fatal("Capture returned an empty checkpoint")
	}

	writeFile(t, dir, "a.go", "package a\n\nvar broken = \n")

	if err := j.Restore(ctx, dir, checkpoint); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "a.go")) //nolint:gosec // fixed fixture filename under t.TempDir()
	if err != nil {
		t.Fatalf("reading restored file: %v", err)
	}
	if string(got) != "package a\n" {
		t.Fatalf("a.go after Restore = %q, want original bytes %q", got, "package a\n")
	}
}

func TestJjRestoreNoopWhenNothingChanged(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	j := vcs.NewJj()
	ctx := context.Background()

	checkpoint, err := j.Capture(ctx, dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if err := j.Restore(ctx, dir, checkpoint); err != nil {
		t.Fatalf("first Restore: %v", err)
	}
	if err := j.Restore(ctx, dir, checkpoint); err != nil {
		t.Fatalf("second Restore (should be a no-op): %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "a.go")) //nolint:gosec // fixed fixture filename under t.TempDir()
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(got) != "package a\n" {
		t.Fatalf("a.go after no-op Restore = %q, want unchanged %q", got, "package a\n")
	}
}

func TestJjChangedFiles(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	j := vcs.NewJj()
	ctx := context.Background()

	checkpoint, err := j.Capture(ctx, dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	writeFile(t, dir, "a.go", "package a\n\nvar X = 1\n")
	writeFile(t, dir, "b.go", "package a\n")

	got, err := j.ChangedFiles(ctx, dir, checkpoint)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}

	want := []string{"a.go", "b.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ChangedFiles = %v, want %v", got, want)
	}
}

func TestJjChangedFilesEmptyMarker(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	writeFile(t, dir, "c.go", "package a\n")

	j := vcs.NewJj()

	got, err := j.ChangedFiles(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}

	want := []string{"a.go", "c.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ChangedFiles(empty marker) = %v, want %v", got, want)
	}
}

// TestJjDiffIgnoresGitDiffExternal proves marker-to-working-copy diff text
// stays unified and git-format even when the colocated repo's local git
// config points diff.external at a command that would fail loudly if jj
// ever shelled out to it: jj never reads git's diff config at all, and
// Diff's --git flag additionally fixes jj's own diff formatter regardless
// of any ui.diff.tool a user's jj config sets.
func TestJjDiffIgnoresGitDiffExternal(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	j := vcs.NewJj()
	ctx := context.Background()

	checkpoint, err := j.Capture(ctx, dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	writeFile(t, dir, "a.go", "package a\n\nvar X = 1\n")

	cmd := exec.CommandContext(ctx, "git", "config", "diff.external", "this-command-does-not-exist-anywhere")
	cmd.Dir = dir
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("git config diff.external: %v: %s", cerr, out)
	}

	got, err := j.Diff(ctx, dir, checkpoint, []string{"a.go"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(got, "+var X = 1") {
		t.Fatalf("Diff missing expected unified hunk, got:\n%s", got)
	}
	if !strings.Contains(got, "--- a/a.go") || !strings.Contains(got, "+++ b/a.go") {
		t.Fatalf("Diff is not unified git format, got:\n%s", got)
	}
}

// DiffStat names the files an undo would discard, and reports an untouched
// tree with a zero count rather than empty output, which is why callers ask
// ChangedFiles whether there is anything to undo at all.
func TestJjDiffStat(t *testing.T) {
	t.Parallel()

	dir := newFixtureRepo(t)
	j := vcs.NewJj()
	ctx := context.Background()

	checkpoint, err := j.Capture(ctx, dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	clean, err := j.DiffStat(ctx, dir, checkpoint)
	if err != nil {
		t.Fatalf("DiffStat on a clean tree: %v", err)
	}
	if !strings.Contains(clean, "0 files changed") {
		t.Fatalf("DiffStat on a clean tree = %q, want a zero count", clean)
	}

	writeFile(t, dir, "a.go", "package a\n\nvar X = 1\n")
	writeFile(t, dir, "b.go", "package a\n")

	dirty, err := j.DiffStat(ctx, dir, checkpoint)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}

	for _, want := range []string{"a.go", "b.go", "2 files changed"} {
		if !strings.Contains(dirty, want) {
			t.Fatalf("DiffStat = %q, want it to mention %q", dirty, want)
		}
	}
}
