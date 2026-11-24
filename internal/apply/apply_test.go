package apply_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/apply"
)

var twoFiles = apply.Fill(`=== a/a.go
%OPEN%
func A() int { return 1 }
%SPLIT%
func A() int { return 2 }
%CLOSE%

%OPEN%
%SPLIT%

func B() {}
%CLOSE%
=== a/new_test.go
%OPEN%
%SPLIT%
package a
%CLOSE%
`)

func TestParse(t *testing.T) {
	t.Parallel()

	edits, err := apply.Parse(twoFiles)
	require.NoError(t, err)
	assert.Equal(t, []apply.Edit{
		{File: "a/a.go", Old: "func A() int { return 1 }\n", New: "func A() int { return 2 }\n"},
		{File: "a/a.go", Old: "", New: "\nfunc B() {}\n"},
		{File: "a/new_test.go", Old: "", New: "package a\n"},
	}, edits)

	for name, text := range map[string]string{
		"nothing":                 "",
		"an edit before any file": "<<<<<<<\na\n=======\nb\n>>>>>>>\n",
		"an edit cut short":       "=== a.go\n<<<<<<<\na\n=======\nb\n",
		"prose between edits":     "=== a.go\nplease change this\n",
	} {
		_, err := apply.Parse(text)
		require.ErrorIs(t, err, apply.ErrEdits, name)
	}
}

func write(t *testing.T, root, name, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func read(t *testing.T, root, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))) //nolint:gosec // a file the test wrote
	require.NoError(t, err)

	return string(body)
}

func TestApply(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	edits, err := apply.Parse(twoFiles)
	require.NoError(t, err)

	files, err := apply.Apply(root, edits)
	require.NoError(t, err)
	assert.Equal(t, []string{"a/a.go", "a/new_test.go"}, files)
	assert.Equal(t, "package a\n\nfunc A() int { return 2 }\n\nfunc B() {}\n", read(t, root, "a/a.go"))
	assert.Equal(t, "package a\n", read(t, root, "a/new_test.go"))
}

func TestApply_WritesNothingUnlessEveryEditApplies(t *testing.T) {
	t.Parallel()

	const before = "package a\n\nfunc A() int { return 1 }\n\nfunc C() int { return 1 }\n"

	tests := []struct {
		name  string
		want  string
		edits []apply.Edit
	}{
		{
			name: "the lines are not there",
			edits: []apply.Edit{
				{File: "a/a.go", Old: "func A() int { return 1 }\n", New: "func A() int { return 2 }\n"},
				{File: "a/a.go", Old: "func A() int {\n\treturn 9\n}\n", New: "x\n"},
			},
			want: "edit 2, in a/a.go: the lines to replace are not in the file; its first line is nowhere",
		},
		{
			name:  "only the indentation differs",
			edits: []apply.Edit{{File: "a/a.go", Old: "  func C() int { return 1 }\n", New: "x\n"}},
			want:  "its first line is at line 5",
		},
		{
			name:  "the lines are there twice",
			edits: []apply.Edit{{File: "a/a.go", Old: " int { return 1 }\n", New: "x\n"}},
			want:  "more than once",
		},
		{
			name:  "a file that is not there",
			edits: []apply.Edit{{File: "a/gone.go", Old: "x\n", New: "y\n"}},
			want:  "edit 1",
		},
		{
			name:  "a path outside the project",
			edits: []apply.Edit{{File: "../b.go", Old: "", New: "y\n"}},
			want:  "not a path under the project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write(t, root, "a/a.go", before)

			_, err := apply.Apply(root, tt.edits)
			require.ErrorIs(t, err, apply.ErrEdits)
			assert.Contains(t, err.Error(), tt.want)
			assert.Equal(t, before, read(t, root, "a/a.go"))
		})
	}
}

func TestApply_MatchesALastLineWithNoNewline(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "a.txt", "one\ntwo")

	_, err := apply.Apply(root, []apply.Edit{{File: "a.txt", Old: "two\n", New: "three\n"}})
	require.NoError(t, err)
	assert.Equal(t, "one\nthree\n", read(t, root, "a.txt"))
}
