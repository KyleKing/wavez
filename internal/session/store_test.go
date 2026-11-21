package session_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/session"
)

const sessionID = "0b7c9c5e-test"

// git runs one git command in dir with an identity that needs no user
// configuration.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	full := append(strings.Fields("-c user.name=t -c user.email=t@example.com -c commit.gpgsign=false"), args...)
	cmd := exec.CommandContext(t.Context(), "git", full...) //nolint:gosec // fixed program, test arguments
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func remove(t *testing.T, root, rel string) {
	t.Helper()
	require.NoError(t, os.Remove(filepath.Join(root, filepath.FromSlash(rel))))
}

// repo returns a git repository holding two committed files.
func repo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "a.go", "package a\n")
	write(t, root, "sub/b.go", "package sub\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "init")

	return root
}

func keep(*session.State) error { return nil }

// scenario is one session: what the tree looked like when it began, what
// happened during it, and what Changed must then report.
type scenario struct {
	name string
	// before runs ahead of the session's first event.
	before func(t *testing.T, root string)
	// during is what happens after the baseline is taken.
	during func(t *testing.T, root string, state *session.State)
	want   []string
}

func runScenarios(t *testing.T, tests []scenario) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			if tt.before != nil {
				tt.before(t, root)
			}

			store := session.NewStore(root)

			_, err := store.Update(t.Context(), sessionID, keep)
			require.NoError(t, err)

			state, err := store.Update(t.Context(), sessionID, func(state *session.State) error {
				if tt.during != nil {
					tt.during(t, root, state)
				}

				return nil
			})
			require.NoError(t, err)

			got, err := store.Changed(t.Context(), state)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStore_Changed_CleanTreeAtStart(t *testing.T) {
	t.Parallel()

	runScenarios(t, []scenario{
		{
			name: "nothing happened",
			want: nil,
		},
		{
			name: "edit tool recorded a file",
			during: func(t *testing.T, root string, state *session.State) {
				t.Helper()
				write(t, root, "a.go", "package a // edited\n")
				state.Record("a.go")
			},
			want: []string{"a.go"},
		},
		{
			name: "shell rewrote a tracked file",
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				write(t, root, "sub/b.go", "package sub // sed\n")
			},
			want: []string{"sub/b.go"},
		},
		{
			name: "generator created an untracked file",
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				write(t, root, "gen/new.go", "package gen\n")
			},
			want: []string{"gen/new.go"},
		},
		{
			name: "tracked file deleted",
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				remove(t, root, "a.go")
			},
			want: []string{"a.go"},
		},
		{
			name: "session committed its change",
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				write(t, root, "a.go", "package a // committed\n")
				write(t, root, "c.go", "package a\n")
				git(t, root, "add", ".")
				git(t, root, "commit", "-qm", "work")
			},
			want: []string{"a.go", "c.go"},
		},
		{
			name: "file replaced by a directory",
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				remove(t, root, "a.go")
				write(t, root, "a.go/inner.go", "package inner\n")
			},
			want: []string{"a.go", "a.go/inner.go"},
		},
	})
}

func TestStore_Changed_DirtyTreeAtStart(t *testing.T) {
	t.Parallel()

	runScenarios(t, []scenario{
		{
			name: "file dirty before the session and left alone",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "a.go", "package a // mine\n")
				write(t, root, "notes.txt", "wip\n")
			},
			want: nil,
		},
		{
			name: "file dirty before the session and changed again",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "a.go", "package a // mine\n")
				write(t, root, "notes.txt", "wip\n")
			},
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				write(t, root, "a.go", "package a // theirs\n")
			},
			want: []string{"a.go"},
		},
		{
			name: "untracked file from before the session deleted",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "notes.txt", "wip\n")
			},
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				remove(t, root, "notes.txt")
			},
			want: []string{"notes.txt"},
		},
		{
			name: "dirty file reverted to the committed content",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "a.go", "package a // mine\n")
			},
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				write(t, root, "a.go", "package a\n")
			},
			want: []string{"a.go"},
		},
		{
			name: "pre-existing dirt committed unchanged",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "a.go", "package a // mine\n")
			},
			during: func(t *testing.T, root string, _ *session.State) {
				t.Helper()
				git(t, root, "commit", "-qam", "theirs")
			},
			want: nil,
		},
		{
			name: "wavez state and ignored files are not changes",
			before: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, ".gitignore", "*.log\n")
				git(t, root, "add", ".gitignore")
				git(t, root, "commit", "-qm", "ignore")
			},
			during: func(t *testing.T, root string, state *session.State) {
				t.Helper()
				write(t, root, "build.log", "noise\n")
				write(t, root, ".wavez/other.json", "{}\n")
				state.Record(".wavez/sessions/x.json")
			},
			want: nil,
		},
	})
}

func TestStore_Changed_RepositoryWithoutCommits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "old.go", "package a\n")

	store := session.NewStore(root)
	state, err := store.Update(t.Context(), sessionID, keep)
	require.NoError(t, err)
	assert.True(t, state.Baseline.Git)
	assert.Empty(t, state.Baseline.Head)

	write(t, root, "new.go", "package a\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "first")
	write(t, root, "later.go", "package a\n")

	got, err := store.Changed(t.Context(), state)
	require.NoError(t, err)
	assert.Equal(t, []string{"later.go", "new.go"}, got)
}

func TestStore_Changed_RootBelowTheRepositoryTop(t *testing.T) {
	t.Parallel()

	top := repo(t)
	root := filepath.Join(top, "sub")
	write(t, top, "sub/dirty.go", "package sub\n")

	store := session.NewStore(root)
	state, err := store.Update(t.Context(), sessionID, keep)
	require.NoError(t, err)
	assert.Equal(t, []string{"dirty.go"}, keys(state.Baseline.Dirty))

	write(t, top, "a.go", "package a // outside the root\n")
	write(t, top, "sub/b.go", "package sub // inside\n")
	git(t, top, "commit", "-qam", "both")
	write(t, top, "sub/c.go", "package sub\n")

	got, err := store.Changed(t.Context(), state)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.go", "c.go"}, got)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}

	return out
}

func TestStore_Changed_OutsideGitKnowsOnlyRecordedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "a.txt", "one\n")

	store := session.NewStore(root)
	state, err := store.Update(t.Context(), sessionID, func(state *session.State) error {
		state.Record("b.txt")
		state.Record("b.txt")
		state.Record("")

		return nil
	})
	require.NoError(t, err)
	assert.False(t, state.Baseline.Git)

	write(t, root, "a.txt", "two\n")

	got, err := store.Changed(t.Context(), state)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.txt"}, got)
}

func TestStore_Update(t *testing.T) {
	t.Parallel()

	t.Run("state survives between calls and leaves no stray files", func(t *testing.T) {
		t.Parallel()

		root := repo(t)
		store := session.NewStore(root)

		_, err := store.Update(t.Context(), sessionID, func(state *session.State) error {
			state.Record("a.go")
			state.Pass("abc")

			return nil
		})
		require.NoError(t, err)

		state, err := store.Update(t.Context(), sessionID, keep)
		require.NoError(t, err)
		assert.Equal(t, []string{"a.go"}, state.Recorded)
		assert.Equal(t, "abc", state.Passed)
		assert.Equal(t, sessionID, state.SessionID)
		assert.NotEmpty(t, state.Baseline.Head)

		entries, err := os.ReadDir(filepath.Dir(store.Path(sessionID)))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, sessionID+".json", entries[0].Name())
	})

	t.Run("an error from the callback writes nothing", func(t *testing.T) {
		t.Parallel()

		store := session.NewStore(repo(t))

		_, err := store.Update(t.Context(), sessionID, func(*session.State) error { return assert.AnError })
		require.ErrorIs(t, err, assert.AnError)
		assert.NoFileExists(t, store.Path(sessionID))
	})

	t.Run("an unreadable state file starts the session over", func(t *testing.T) {
		t.Parallel()

		for _, content := range []string{"{not json", `{"version": 999, "recorded": ["x"]}`} {
			store := session.NewStore(repo(t))
			require.NoError(t, os.MkdirAll(filepath.Dir(store.Path(sessionID)), 0o750))
			require.NoError(t, os.WriteFile(store.Path(sessionID), []byte(content), 0o600))

			state, err := store.Update(t.Context(), sessionID, keep)
			require.NoError(t, err)
			assert.Equal(t, session.Version, state.Version)
			assert.Empty(t, state.Recorded)
		}
	})

	t.Run("concurrent hooks do not lose each other's records", func(t *testing.T) {
		t.Parallel()

		store := session.NewStore(repo(t))
		names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}

		var wg sync.WaitGroup
		for _, name := range names {
			wg.Go(func() {
				_, err := store.Update(context.WithoutCancel(t.Context()), sessionID, func(state *session.State) error {
					state.Record(name)

					return nil
				})
				assert.NoError(t, err)
			})
		}

		wg.Wait()

		state, err := store.Update(t.Context(), sessionID, keep)
		require.NoError(t, err)
		assert.Equal(t, names, state.Recorded)
	})
}

func TestStore_Update_RejectsIDsThatAreNotFileNames(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", ".", "..", "../escape", `a\b`, "a/b", "nul\x00"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			_, err := session.NewStore(t.TempDir()).Update(t.Context(), id, keep)
			require.ErrorIs(t, err, session.ErrInvalidID)
		})
	}
}

func lockPath(store *session.Store) string {
	return strings.TrimSuffix(store.Path(sessionID), ".json") + ".lock"
}

func TestStore_Update_Lock(t *testing.T) {
	t.Parallel()

	t.Run("a lock left by a killed hook is taken over", func(t *testing.T) {
		t.Parallel()

		store := session.NewStore(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Dir(lockPath(store)), 0o750))
		require.NoError(t, os.WriteFile(lockPath(store), nil, 0o600))

		old := time.Now().Add(-time.Hour)
		require.NoError(t, os.Chtimes(lockPath(store), old, old))

		_, err := store.Update(t.Context(), sessionID, keep)
		require.NoError(t, err)
		assert.NoFileExists(t, lockPath(store))
	})

	t.Run("a held lock gives up when the hook is canceled", func(t *testing.T) {
		t.Parallel()

		store := session.NewStore(t.TempDir())
		require.NoError(t, os.MkdirAll(filepath.Dir(lockPath(store)), 0o750))
		require.NoError(t, os.WriteFile(lockPath(store), nil, 0o600))

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		_, err := store.Update(ctx, sessionID, keep)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NoFileExists(t, store.Path(sessionID))
	})
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "a.go", "one\n")
	write(t, root, "b.go", "two\n")
	require.NoError(t, os.Symlink("a.go", filepath.Join(root, "link")))

	files := []string{"b.go", "a.go", "link", "gone.go", "a.go/below"}

	first, err := session.Fingerprint(root, files)
	require.NoError(t, err)

	reordered, err := session.Fingerprint(root, []string{"link", "a.go/below", "gone.go", "a.go", "b.go"})
	require.NoError(t, err)
	assert.Equal(t, first, reordered, "order of the paths must not matter")

	subset, err := session.Fingerprint(root, []string{"a.go"})
	require.NoError(t, err)
	assert.NotEqual(t, first, subset)

	write(t, root, "b.go", "three\n")

	edited, err := session.Fingerprint(root, files)
	require.NoError(t, err)
	assert.NotEqual(t, first, edited)

	write(t, root, "b.go", "two\n")

	restored, err := session.Fingerprint(root, files)
	require.NoError(t, err)
	assert.Equal(t, first, restored)

	remove(t, root, "b.go")

	deleted, err := session.Fingerprint(root, files)
	require.NoError(t, err)
	assert.NotEqual(t, first, deleted)
}

func TestStore_Changed_BaselineCommitNoLongerExists(t *testing.T) {
	t.Parallel()

	root := repo(t)
	store := session.NewStore(root)

	state, err := store.Update(t.Context(), sessionID, keep)
	require.NoError(t, err)

	state.Baseline.Head = "0123456789012345678901234567890123456789"

	write(t, root, "a.go", "package a // edited\n")

	got, err := store.Changed(t.Context(), state)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go"}, got)
}
