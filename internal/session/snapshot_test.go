package session_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/session"
)

// capture runs git in dir and returns its trimmed stdout.
func capture(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed program, test arguments
	cmd.Dir = dir

	out, err := cmd.Output()
	require.NoError(t, err, "git %s", strings.Join(args, " "))

	return strings.TrimSpace(string(out))
}

func baseline(t *testing.T, root string) session.Baseline {
	t.Helper()

	state, err := session.NewStore(root).Update(t.Context(), sessionID, keep)
	require.NoError(t, err)

	return state.Baseline
}

func TestBaseline_TreeHoldsWhatWasUncommitted(t *testing.T) {
	t.Parallel()

	root := repo(t)
	write(t, root, "a.go", "package a // edited before the session\n")
	write(t, root, "new file.go", "package a // untracked\n")
	write(t, root, "*.go", "package a // a name that is also a pattern\n")
	remove(t, root, "sub/b.go")
	write(t, root, ".wavez/.gitignore", "*\n")

	status := capture(t, root, "status", "--porcelain")
	base := baseline(t, root)

	require.NotEmpty(t, base.Tree)
	assert.True(t, base.Restorable())
	assert.Equal(t, status, capture(t, root, "status", "--porcelain"), "the index and the tree are left alone")

	files := strings.Split(capture(t, root, "ls-tree", "-r", "--name-only", "-z", base.Tree), "\x00")
	assert.ElementsMatch(t, []string{"*.go", "a.go", "new file.go", ""}, files)
	assert.Equal(t, "package a // edited before the session", capture(t, root, "cat-file", "-p", base.Tree+":a.go"))
}

func TestBaseline_TreeFromBelowTheRepositoryTop(t *testing.T) {
	t.Parallel()

	top := repo(t)
	write(t, top, "sub/b.go", "package sub // edited\n")
	write(t, top, "a.go", "package a // outside the root\n")

	base := baseline(t, filepath.Join(top, "sub"))

	require.NotEmpty(t, base.Tree)
	assert.Equal(t, "package sub // edited", capture(t, top, "cat-file", "-p", base.Tree+":sub/b.go"))
	assert.Equal(t, "package a", capture(t, top, "cat-file", "-p", base.Tree+":a.go"),
		"dirt outside the root is not the root's baseline")
}

func TestBaseline_Restorable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		before func(t *testing.T, root string)
		name   string
		tree   bool
		want   bool
	}{
		{name: "a clean tree is its commit and needs no tree", want: true},
		{
			name: "dirt too large to copy is not kept",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "big.bin", strings.Repeat("x", 9<<20))
			},
		},
		{
			name: "a repository with no commit has nothing to restore",
			before: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t, os.RemoveAll(filepath.Join(root, ".git")))
				git(t, root, "init", "-q")
			},
		},
		{
			name: "outside git there is no baseline to restore",
			before: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t, os.RemoveAll(filepath.Join(root, ".git")))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			if tt.before != nil {
				tt.before(t, root)
			}

			base := baseline(t, root)

			assert.Equal(t, tt.tree, base.Tree != "")
			assert.Equal(t, tt.want, base.Restorable())
		})
	}
}

func TestBaseline_AStateFromBeforeTheTreeIsStillRead(t *testing.T) {
	t.Parallel()

	root := repo(t)
	store := session.NewStore(root)
	old := `{"session_id":"` + sessionID + `","baseline":{"dirty":{"a.go":"00"},"head":"abc","git":true},` +
		`"failure":{"rounds":0},"recorded":["a.go"],"version":1,"chain":0}`

	require.NoError(t, os.MkdirAll(filepath.Dir(store.Path(sessionID)), 0o750))
	require.NoError(t, os.WriteFile(store.Path(sessionID), []byte(old), 0o600))

	state, err := store.Update(t.Context(), sessionID, keep)
	require.NoError(t, err)

	assert.Equal(t, []string{"a.go"}, state.Recorded, "the session is kept, not started over")
	assert.False(t, state.Baseline.Restorable(), "dirt with no tree cannot be rebuilt")
}
