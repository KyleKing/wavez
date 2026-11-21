package bench_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/scripttest"
)

// gitIn runs git in dir with an identity, so a commit works on a machine
// that has none configured.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	full := append([]string{"-c", "user.name=bench", "-c", "user.email=bench@example.invalid"}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...) //nolint:gosec // git, with the test's own arguments
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return strings.TrimSpace(string(out))
}

// newRepo makes a repository with two commits and returns it with the hash
// of the first, so a workspace at that hash is visibly not the tip.
func newRepo(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	gitIn(t, dir, "init", "--quiet")
	write(t, filepath.Join(dir, "README.md"), "Status: first.\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "--quiet", "-m", "first")

	rev := gitIn(t, dir, "rev-parse", "HEAD")

	write(t, filepath.Join(dir, "README.md"), "Status: second.\n")
	gitIn(t, dir, "commit", "--quiet", "-am", "second")

	return dir, rev
}

func write(t *testing.T, path, body string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, name, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	scripttest.Write(t, path, "#!/bin/sh\n"+body)

	return path
}

func read(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // a path the test made
	require.NoError(t, err)

	return string(body)
}

// fakeHarness answers a run from a function, and remembers what it was
// asked.
type fakeHarness struct {
	run  func(ctx context.Context, req bench.Request) (bench.Outcome, error)
	reqs []bench.Request
}

func (f *fakeHarness) Run(ctx context.Context, req bench.Request) (bench.Outcome, error) {
	f.reqs = append(f.reqs, req)

	return f.run(ctx, req)
}

func mustChecks(t *testing.T, texts ...string) []bench.Check {
	t.Helper()

	checks := make([]bench.Check, 0, len(texts))

	for _, text := range texts {
		c, err := bench.ParseCheck(text)
		require.NoError(t, err)

		checks = append(checks, c)
	}

	return checks
}
