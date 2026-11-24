package lane_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/lane"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed program, test arguments
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func read(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // a file the test wrote
	require.NoError(t, err)

	return string(body)
}

// repo has two commits on a file under sub/, tagged one and two, and an
// uncommitted third version in the working tree.
func repo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH")
	}

	root := t.TempDir()
	run(t, root, "init", "-q", "-b", "main")

	for _, version := range []string{"one", "two"} {
		write(t, filepath.Join(root, "sub", "f.txt"), version+"\n")
		run(t, root, "add", "-A")
		run(t, root, "commit", "-qm", version)
		run(t, root, "tag", version)
	}

	write(t, filepath.Join(root, "sub", "f.txt"), "half done\n")

	return root
}

func TestOpen(t *testing.T) {
	t.Parallel()

	root, cache := repo(t), t.TempDir()

	first, err := lane.Open(t.Context(), filepath.Join(root, "sub"), cache, "one")
	require.NoError(t, err)
	assert.Equal(t, "one\n", read(t, filepath.Join(first.Root, "f.txt")), "the lane is at the revision asked for")
	assert.Equal(t, filepath.Join(first.Top, "sub"), first.Root, "the root keeps its place in the repository")
	assert.Equal(t, "half done\n", read(t, filepath.Join(root, "sub", "f.txt")), "the checkout is not touched")

	// Work in the lane is kept while the lane stays at its revision.
	write(t, filepath.Join(first.Root, "f.txt"), "one, fixed\n")

	again, err := lane.Open(t.Context(), root, cache, "one")
	require.NoError(t, err)
	assert.Equal(t, first.Top, again.Top)
	assert.Equal(t, "one, fixed\n", read(t, filepath.Join(again.Top, "sub", "f.txt")))

	// A second revision is a second lane.
	second, err := lane.Open(t.Context(), root, cache, "two")
	require.NoError(t, err)
	assert.NotEqual(t, first.Top, second.Top)
	assert.Equal(t, "two\n", read(t, filepath.Join(second.Top, "sub", "f.txt")))
}

func TestOpen_FollowsARevisionThatMoved(t *testing.T) {
	t.Parallel()

	root, cache := repo(t), t.TempDir()
	run(t, root, "branch", "review", "one")

	opened, err := lane.Open(t.Context(), root, cache, "review")
	require.NoError(t, err)

	// With work in it, a lane is not moved out from under that work.
	write(t, filepath.Join(opened.Top, "sub", "f.txt"), "one, half fixed\n")
	run(t, root, "branch", "-f", "review", "two")

	_, err = lane.Open(t.Context(), root, cache, "review")
	require.ErrorIs(t, err, lane.ErrBusy)

	// Clean again, it follows.
	run(t, opened.Top, "checkout", "-q", "--", ".")

	moved, err := lane.Open(t.Context(), root, cache, "review")
	require.NoError(t, err)
	assert.Equal(t, opened.Top, moved.Top)
	assert.Equal(t, "two\n", read(t, filepath.Join(moved.Top, "sub", "f.txt")))
}

func TestOpen_RefusesWhatIsNotARevision(t *testing.T) {
	t.Parallel()

	_, err := lane.Open(t.Context(), repo(t), t.TempDir(), "no-such-rev")
	require.ErrorContains(t, err, "resolving no-such-rev")

	_, err = lane.Open(t.Context(), t.TempDir(), t.TempDir(), "HEAD")
	require.ErrorContains(t, err, "finding the repository")
}

func TestOpen_KeepsCommitsMadeInTheLane(t *testing.T) {
	t.Parallel()

	root, cache := repo(t), t.TempDir()
	run(t, root, "branch", "review", "one")

	opened, err := lane.Open(t.Context(), root, cache, "review")
	require.NoError(t, err)

	// The fix is committed in the lane. The branch in the checkout has not
	// moved, and asking for it again must not take the lane back.
	write(t, filepath.Join(opened.Top, "sub", "f.txt"), "one, fixed\n")
	run(t, opened.Top, "commit", "-qam", "fix")

	again, err := lane.Open(t.Context(), root, cache, "review")
	require.NoError(t, err)
	assert.Equal(t, "one, fixed\n", read(t, filepath.Join(again.Top, "sub", "f.txt")))

	// A revision that left those commits behind does not get the lane.
	run(t, root, "branch", "-f", "review", "two")

	_, err = lane.Open(t.Context(), root, cache, "review")
	require.ErrorIs(t, err, lane.ErrDiverged)
}

func TestOpen_RevisionsThatDifferOnlyInPunctuationGetTheirOwnLanes(t *testing.T) {
	t.Parallel()

	root, cache := repo(t), t.TempDir()
	run(t, root, "branch", "a/b", "one")
	run(t, root, "branch", "a-b", "two")

	slash, err := lane.Open(t.Context(), root, cache, "a/b")
	require.NoError(t, err)

	dash, err := lane.Open(t.Context(), root, cache, "a-b")
	require.NoError(t, err)

	assert.NotEqual(t, slash.Top, dash.Top)
	assert.Equal(t, "one\n", read(t, filepath.Join(slash.Top, "sub", "f.txt")))
	assert.Equal(t, "two\n", read(t, filepath.Join(dash.Top, "sub", "f.txt")))
}
