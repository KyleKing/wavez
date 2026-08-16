package vcs_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/vcs"
)

//nolint:gocritic // named returns here would trip nonamedreturns instead; callers read (dir, firstSHA)
func newFixtureRepo(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "Test")

	writeFile(t, dir, "a.go", "package a\n")
	run(t, dir, "add", "a.go")
	run(t, dir, "commit", "-q", "-m", "initial")
	firstSHA := strings.TrimSpace(runOut(t, dir, "rev-parse", "HEAD"))

	return dir, firstSHA
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()

	if _, err := runGitCmd(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func runOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	out, err := runGitCmd(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return out
}

func runGitCmd(dir string, args ...string) (string, error) {
	//nolint:gosec // args are this test's own fixed fixture-setup commands
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")

	out, err := cmd.CombinedOutput()

	return string(out), err
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

func TestGitRepoRoot(t *testing.T) {
	t.Parallel()

	dir, _ := newFixtureRepo(t)
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	g := vcs.NewGit()

	got, err := g.RepoRoot(context.Background(), sub)
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

func TestGitChangedFiles(t *testing.T) {
	t.Parallel()

	dir, firstSHA := newFixtureRepo(t)

	writeFile(t, dir, "a.go", "package a\n\nvar X = 1\n")
	writeFile(t, dir, "b.go", "package a\n")

	g := vcs.NewGit()

	got, err := g.ChangedFiles(context.Background(), dir, firstSHA)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}

	want := []string{"a.go", "b.go"}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ChangedFiles = %v, want %v", got, want)
	}
}

func TestGitChangedFilesEmptyMarker(t *testing.T) {
	t.Parallel()

	dir, _ := newFixtureRepo(t)
	writeFile(t, dir, "c.go", "package a\n")

	g := vcs.NewGit()

	got, err := g.ChangedFiles(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}

	want := []string{"a.go", "c.go"}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ChangedFiles(empty marker) = %v, want %v", got, want)
	}
}

func TestGitDiff(t *testing.T) {
	t.Parallel()

	dir, firstSHA := newFixtureRepo(t)
	writeFile(t, dir, "a.go", "package a\n\nvar X = 1\n")

	g := vcs.NewGit()

	got, err := g.Diff(context.Background(), dir, firstSHA, []string{"a.go"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(got, "+var X = 1") {
		t.Fatalf("Diff missing expected hunk, got:\n%s", got)
	}
}
