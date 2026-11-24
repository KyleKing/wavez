package bench_test

import (
	"os"
	"path/filepath"
	"strings"
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

			ws, err := bench.NewWorkspace(t.Context(), tt.repo, first, temp, "")
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

	ws, err := bench.NewWorkspace(t.Context(), "file://"+repo, first, temp, "")
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

			_, err := bench.NewWorkspace(t.Context(), tt.repo, tt.rev, temp, "")
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

	_, err := bench.NewWorkspace(t.Context(), repo, first, filepath.Join(t.TempDir(), "absent"), "")
	require.Error(t, err)
}

func TestWorkspace_URLMirrorIsFetchedOnceAndKept(t *testing.T) {
	t.Parallel()

	repo, first := newRepo(t)
	second := gitIn(t, repo, "rev-parse", "HEAD")
	cache := t.TempDir()
	url := "file://" + repo

	ws, err := bench.NewWorkspace(t.Context(), url, first, t.TempDir(), cache)
	require.NoError(t, err)
	assert.Equal(t, "Status: first.\n", read(t, filepath.Join(ws.Dir, "README.md")))
	require.NoError(t, ws.Remove(t.Context()))

	mirrors, err := filepath.Glob(filepath.Join(cache, "*.git"))
	require.NoError(t, err)
	require.Len(t, mirrors, 1, "one mirror per URL")
	assert.Equal(t, "true", gitIn(t, mirrors[0], "rev-parse", "--is-shallow-repository"),
		"a full hash is fetched alone, not with its history")
	assert.NotContains(t, gitIn(t, mirrors[0], "worktree", "list"), ws.Dir)

	// The same URL at another commit reuses the mirror and fetches into it.
	ws, err = bench.NewWorkspace(t.Context(), url, second, t.TempDir(), cache)
	require.NoError(t, err)
	assert.Equal(t, "Status: second.\n", read(t, filepath.Join(ws.Dir, "README.md")))
	require.NoError(t, ws.Remove(t.Context()))

	again, err := filepath.Glob(filepath.Join(cache, "*"))
	require.NoError(t, err)
	assert.Equal(t, mirrors, again, "nothing but the mirror is left in the cache")

	// With the source gone, a commit the mirror holds still checks out.
	require.NoError(t, os.RemoveAll(repo))

	ws, err = bench.NewWorkspace(t.Context(), url, first, t.TempDir(), cache)
	require.NoError(t, err)
	assert.Equal(t, first, gitIn(t, ws.Dir, "rev-parse", "HEAD"))
	require.NoError(t, ws.Remove(t.Context()))
}

func TestWorkspace_URLAtAnAbbreviatedRevFetchesTheBranches(t *testing.T) {
	t.Parallel()

	repo, first := newRepo(t)
	cache := t.TempDir()

	ws, err := bench.NewWorkspace(t.Context(), "file://"+repo, first[:7], t.TempDir(), cache)
	require.NoError(t, err)
	assert.Equal(t, first, gitIn(t, ws.Dir, "rev-parse", "HEAD"))
	require.NoError(t, ws.Remove(t.Context()))
}

func TestWorkspace_URLWithAnUnknownRevLeavesOnlyTheMirror(t *testing.T) {
	t.Parallel()

	repo, _ := newRepo(t)
	cache := t.TempDir()
	temp := t.TempDir()

	_, err := bench.NewWorkspace(t.Context(), "file://"+repo, "0000000000000000000000000000000000000000", temp, cache)
	require.ErrorContains(t, err, "no commit")

	left, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, left)

	mirrors, err := filepath.Glob(filepath.Join(cache, "*"))
	require.NoError(t, err)
	assert.Len(t, mirrors, 1)
	assert.True(t, strings.HasSuffix(mirrors[0], ".git"), "no half-made mirror is left: %v", mirrors)
}
