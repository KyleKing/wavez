package bench_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

func TestWorkspace_WorktreeAtThePinnedRev(t *testing.T) {
	t.Parallel()

	repo, first := newRepo(t)
	sub := filepath.Join(repo, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o750))

	tests := []struct {
		name string
		repo string
	}{
		{name: "repository root", repo: repo},
		{name: "a directory inside it", repo: sub},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			temp := t.TempDir()

			ws, err := bench.NewWorkspace(t.Context(), tt.repo, first, temp)
			require.NoError(t, err)

			assert.Equal(t, "Status: first.\n", read(t, filepath.Join(ws.Dir, "README.md")))
			assert.Equal(t, first, gitIn(t, ws.Dir, "rev-parse", "HEAD"))
			assert.Contains(t, gitIn(t, repo, "worktree", "list"), ws.Dir)

			require.NoError(t, ws.Remove(t.Context()))

			assert.NoDirExists(t, ws.Dir)
			assert.NotContains(t, gitIn(t, repo, "worktree", "list"), ws.Dir)

			left, err := os.ReadDir(temp)
			require.NoError(t, err)
			assert.Empty(t, left)
		})
	}
}

func TestWorkspace_CloneOfAURL(t *testing.T) {
	t.Parallel()

	repo, first := newRepo(t)
	temp := t.TempDir()

	ws, err := bench.NewWorkspace(t.Context(), "file://"+repo, first, temp)
	require.NoError(t, err)

	assert.Equal(t, "Status: first.\n", read(t, filepath.Join(ws.Dir, "README.md")))
	assert.NotContains(t, gitIn(t, repo, "worktree", "list"), ws.Dir)

	require.NoError(t, ws.Remove(t.Context()))
	assert.NoDirExists(t, ws.Dir)
}

func TestWorkspace_FailuresLeaveNothingBehind(t *testing.T) {
	t.Parallel()

	repo, _ := newRepo(t)

	tests := []struct {
		name string
		repo string
		rev  string
	}{
		{name: "unknown rev", repo: repo, rev: "0000000000000000000000000000000000000000"},
		{name: "not a repository", repo: t.TempDir(), rev: "abc1234"},
		{name: "unreachable url", repo: "file:///nonexistent/repo.git", rev: "abc1234"},
		{name: "url with an unknown rev", repo: "file://" + repo, rev: "0000000000000000000000000000000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			temp := t.TempDir()

			_, err := bench.NewWorkspace(t.Context(), tt.repo, tt.rev, temp)
			require.Error(t, err)

			left, readErr := os.ReadDir(temp)
			require.NoError(t, readErr)
			assert.Empty(t, left)
		})
	}
}

func TestWorkspace_UnusableTempDir(t *testing.T) {
	t.Parallel()

	repo, first := newRepo(t)

	_, err := bench.NewWorkspace(t.Context(), repo, first, filepath.Join(t.TempDir(), "absent"))
	require.Error(t, err)
}
