package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/session"
)

// fakeJJ writes a stand-in jj into a directory of its own and returns it,
// for a PATH holding nothing else, so git finds no repository and the
// workspace at ws is read through jj. Asked about the working-copy
// commit it answers c0ffee, and a diff from c0ffee lists diff.
func fakeJJ(t *testing.T, ws, diff string) string {
	t.Helper()

	bin := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --color=never ] && shift\ncase \"$1\" in\n" +
		"log) echo c0ffee ;;\n" +
		"root) echo '" + ws + "' ;;\n" +
		"diff) [ \"$3\" = c0ffee ] || exit 3; printf '%s\\n' " + diff + " ;;\n" +
		"*) exit 2 ;;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "jj"), []byte(script), 0o700)) //nolint:gosec // a test script

	return bin
}

// The fake jj replaces PATH for the whole process, so this does not run in
// parallel.
func TestStore_Changed_AJJWorkspaceWithoutGit(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	write(t, ws, ".jj/repo/store/git/HEAD", "ref: refs/heads/main\n")
	write(t, ws, "sub/gen.go", "package sub\n")
	t.Setenv("PATH", fakeJJ(t, ws, "shell.go sub/gen.go"))

	store := session.NewStore(ws)
	state, err := store.Update(t.Context(), sessionID, func(state *session.State) error {
		state.Record("edited.go")

		return nil
	})
	require.NoError(t, err)
	assert.False(t, state.Baseline.Git)
	assert.Equal(t, "c0ffee", state.Baseline.JJ)
	assert.False(t, state.Baseline.Restorable(), "a jj baseline has no git tree to compare a failure against")

	got, err := store.Changed(t.Context(), state)
	require.NoError(t, err)
	assert.Equal(t, []string{"edited.go", "shell.go", "sub/gen.go"}, got,
		"a file a shell command wrote is in the change set, as it is under git")

	got, err = session.NewStore(filepath.Join(ws, "sub")).Changed(t.Context(), state)
	require.NoError(t, err)
	assert.Equal(t, []string{"edited.go", "gen.go"}, got)
}
