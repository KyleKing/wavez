package goal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/goal"
)

func read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // a file this test wrote under its own temporary directory
	require.NoError(t, err)

	return string(data)
}

func TestInit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	file, err := goal.Init(root, `  Say "no" to an empty pattern `, true, false)
	require.NoError(t, err)
	assert.Equal(t, goal.Path(root), file)
	assert.Equal(t, "*\n", read(t, filepath.Join(root, ".wavez", ".gitignore")), "the goal stays out of git")

	loaded, err := goal.Load(root)
	require.NoError(t, err)
	assert.Equal(t, goal.Goal{Sentence: `Say "no" to an empty pattern`, Block: true}, loaded)

	_, err = goal.Init(root, "another", false, false)
	require.ErrorIs(t, err, goal.ErrExists)

	require.NoError(t, os.WriteFile(goal.StatePath(root), []byte("{}"), 0o600))

	_, err = goal.Init(root, "another", false, true)
	require.NoError(t, err)
	assert.NoFileExists(t, goal.StatePath(root), "a replaced goal's verdicts go with it")

	loaded, err = goal.Load(root)
	require.NoError(t, err)
	assert.Equal(t, goal.Goal{Sentence: "another"}, loaded)

	for _, sentence := range []string{"", "  ", "two\nlines"} {
		_, err = goal.Init(t.TempDir(), sentence, false, false)

		var invalid *goal.Error

		require.ErrorAs(t, err, &invalid, "%q", sentence)
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	_, err := goal.Add(root, goal.Addition{Kind: goal.KindFile, Args: []string{"a"}})
	require.ErrorIs(t, err, goal.ErrNoGoal)

	// A hand-written file with a comment and no final newline.
	const written = "# mine\ngoal = \"g\" # keep this"

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wavez"), 0o750))
	require.NoError(t, os.WriteFile(goal.Path(root), []byte(written), 0o600))

	additions := []goal.Addition{
		{Kind: goal.KindTest, Args: []string{"./internal/config -run TestParseGlob_Empty"}, Name: "error returned"},
		{Kind: goal.KindTest, Args: []string{"./p", "-run", "TestOne"}, Timeout: "30s"},
		{Kind: goal.KindGrep, Args: []string{"README.md", "empty pattern"}},
		{Kind: goal.KindAbsent, Args: []string{"internal/**/*.go", `TODO\(glob\)`}, Name: `no "TODO" left`},
		{Kind: goal.KindFile, Args: []string{"docs/glob.md"}},
		{Kind: goal.KindCommand, Args: []string{"golangci-lint", "run", "./..."}, Name: "lints clean"},
	}

	for _, addition := range additions {
		added, err := goal.Add(root, addition)
		require.NoError(t, err)
		assert.Equal(t, addition.Kind, added.Kind)
	}

	assert.Equal(t, written+`

[[done]]
name = "error returned"
test = "./internal/config -run TestParseGlob_Empty"

[[done]]
name = "test ./p -run TestOne"
test = "./p -run TestOne"
timeout = "30s"

[[done]]
name = "grep README.md empty pattern"
grep = { file = "README.md", pattern = "empty pattern" }

[[done]]
name = "no \"TODO\" left"
absent = { glob = "internal/**/*.go", pattern = 'TODO\(glob\)' }

[[done]]
name = "file docs/glob.md"
file = "docs/glob.md"

[[done]]
name = "lints clean"
command = "golangci-lint run ./..."
`, read(t, goal.Path(root)))

	loaded, err := goal.Load(root)
	require.NoError(t, err)
	require.Len(t, loaded.Items, 6)
	assert.Equal(t, `TODO\(glob\)`, loaded.Items[3].Pattern.String())
}

func TestAdd_RefusedLeavesTheFileAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		add  goal.Addition
	}{
		{name: "an unknown kind", add: goal.Addition{Kind: "lint", Args: []string{"x"}}, want: `"lint" is not a kind`},
		{
			name: "a grep with one word", add: goal.Addition{Kind: goal.KindGrep, Args: []string{"x"}},
			want: "takes a file and a pattern",
		},
		{name: "a file with two", add: goal.Addition{Kind: goal.KindFile, Args: []string{"a", "b"}}, want: "one path"},
		{name: "a command with none", add: goal.Addition{Kind: goal.KindCommand}, want: "needs a value"},
		{
			name: "a path that escapes", add: goal.Addition{Kind: goal.KindFile, Args: []string{"../x"}},
			want: "inside the project",
		},
		{
			name: "an unprefixed package", add: goal.Addition{Kind: goal.KindTest, Args: []string{"internal/x"}},
			want: "want ./internal/x",
		},
		{
			name: "a pattern that does not compile", add: goal.Addition{Kind: goal.KindGrep, Args: []string{"a", "("}},
			want: "not a regular expression",
		},
		{
			name: "a name already used", add: goal.Addition{Kind: goal.KindFile, Args: []string{"b"}, Name: "first"},
			want: "is used twice",
		},
		{
			name: "a timeout on a file", add: goal.Addition{Kind: goal.KindFile, Args: []string{"b"}, Timeout: "1m"},
			want: "only a test or a command",
		},
		{
			name: "a control character", add: goal.Addition{Kind: goal.KindCommand, Args: []string{"a\nb"}},
			want: "control character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const written = header + "\n[[done]]\nname = \"first\"\nfile = \"a\"\n"

			root := project(t, map[string]string{goalFile: written})

			_, err := goal.Add(root, tt.add)
			require.ErrorContains(t, err, tt.want)
			assert.Equal(t, written, read(t, goal.Path(root)))
		})
	}
}

func TestClear(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{goalFile: header + item("passes", `command = "true"`)})
	evaluate(t, root, true)
	require.FileExists(t, goal.StatePath(root))

	had, err := goal.Clear(root)
	require.NoError(t, err)
	assert.True(t, had)
	assert.NoFileExists(t, goal.Path(root))
	assert.NoFileExists(t, goal.StatePath(root))

	had, err = goal.Clear(root)
	require.NoError(t, err)
	assert.False(t, had)
}
