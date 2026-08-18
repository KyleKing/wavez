package snippet_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/snippet"
)

func writeJSON(t *testing.T, path string, snippets map[string]string) {
	t.Helper()

	data, err := json.Marshal(snippets)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestLoadAll(t *testing.T) {
	tests := []struct {
		setup   func(t *testing.T, root string, userPath func() string)
		want    map[string]string
		name    string
		wantErr bool
	}{
		{
			name: "no files is not an error",
			setup: func(t *testing.T, _ string, _ func() string) {
				t.Helper()
			},
			want: map[string]string{},
		},
		{
			name: "repo only",
			setup: func(t *testing.T, root string, _ func() string) {
				t.Helper()
				writeJSON(t, snippet.RepoPath(root), map[string]string{"qt": "use the question tool liberally"})
			},
			want: map[string]string{"qt": "use the question tool liberally"},
		},
		{
			name: "user only",
			setup: func(t *testing.T, _ string, userPath func() string) {
				t.Helper()
				writeJSON(t, userPath(), map[string]string{"sig": "thanks, Kyle"})
			},
			want: map[string]string{"sig": "thanks, Kyle"},
		},
		{
			name: "repo wins on a name clash",
			setup: func(t *testing.T, root string, userPath func() string) {
				t.Helper()
				writeJSON(t, userPath(), map[string]string{"qt": "user text", "sig": "thanks, Kyle"})
				writeJSON(t, snippet.RepoPath(root), map[string]string{"qt": "repo text"})
			},
			want: map[string]string{"qt": "repo text", "sig": "thanks, Kyle"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", "")

			userPath, err := snippet.UserPath()
			require.NoError(t, err)

			tt.setup(t, root, func() string { return userPath })

			got, err := snippet.LoadAll(root)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSave_WritesAtomicallyAndRoundTrips(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "snippets.json")
	snippets := map[string]string{"qt": "use the question tool liberally"}

	require.NoError(t, snippet.Save(path, snippets))

	data, err := os.ReadFile(path) // #nosec G304 -- path is a t.TempDir() fixture
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, snippets, got)

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no leftover temp file after rename")
}
