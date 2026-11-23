package goal_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/goal"
)

// project writes files under a fresh root and returns it. The goal file is
// the entry named by goalFile.
func project(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}

	return root
}

const goalFile = ".wavez/goal.toml"

const header = "goal = \"ParseGlob rejects an empty pattern\"\n"

func TestLoad(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{goalFile: header + `block = true

[[done]]
name = "error returned"
test = "./internal/config ./internal/gate -run TestParseGlob_Empty"
timeout = "30s"

[[done]]
name = "documented"
grep = { file = "./README.md", pattern = "empty pattern" }

[[done]]
name = "no TODO left"
absent = { glob = "internal/config/**/*.go", pattern = 'TODO\(glob\)' }

[[done]]
name = "file exists"
file = "docs/glob.md"

[[done]]
name = "lints clean"
command = "golangci-lint run ./internal/config/..."
`})

	got, err := goal.Load(root)
	require.NoError(t, err)

	assert.Equal(t, "ParseGlob rejects an empty pattern", got.Sentence)
	assert.True(t, got.Block)
	require.Len(t, got.Items, 5)

	assert.Equal(t, goal.KindTest, got.Items[0].Kind)
	assert.Equal(t, []string{"./internal/config", "./internal/gate"}, got.Items[0].Packages)
	assert.Equal(t, "TestParseGlob_Empty", got.Items[0].Run)
	assert.Equal(t, 30*time.Second, got.Items[0].Timeout)

	assert.Equal(t, goal.KindGrep, got.Items[1].Kind)
	assert.Equal(t, "README.md", got.Items[1].File)
	assert.Equal(t, "empty pattern", got.Items[1].Pattern.String())

	assert.Equal(t, goal.KindAbsent, got.Items[2].Kind)
	assert.True(t, got.Items[2].Glob.Match("internal/config/sub/glob.go"))

	assert.Equal(t, goal.KindFile, got.Items[3].Kind)
	assert.Equal(t, goal.KindCommand, got.Items[4].Kind)
	assert.Equal(t, goal.DefaultTimeout, got.Items[4].Timeout)
}

func TestLoad_NoFile(t *testing.T) {
	t.Parallel()

	_, err := goal.Load(t.TempDir())
	require.ErrorIs(t, err, goal.ErrNoGoal)
}

func TestLoad_Invalid(t *testing.T) {
	t.Parallel()

	item := func(body string) string { return header + "\n[[done]]\nname = \"it\"\n" + body + "\n" }

	tests := []struct {
		name string
		file string
		want string
		line int
	}{
		{name: "no sentence", file: "block = true\n", want: "goal must be one sentence"},
		{name: "a syntax error names its line", file: header + "block = \n", want: "goal.toml:2: ", line: 2},
		{name: "an unknown top-level key", file: header + "blocks = true\n", want: "blocks is not a known", line: 2},
		{
			name: "an unknown item key", file: item("file = \"a\"\ntimeouts = \"2m\""),
			want: "done.timeouts is not a known key", line: 6,
		},
		{
			name: "an unknown key inside a table", file: item(`grep = { file = "a", patterns = "x" }`),
			want: "done.grep.patterns is not a known key", line: 5,
		},
		{name: "no name", file: header + "[[done]]\nfile = \"a\"\n", want: "done[0].name must be a name"},
		{name: "no check", file: item(""), want: `done "it" has no check`},
		{name: "two checks", file: item("file = \"a\"\ncommand = \"true\""), want: "has 2 checks (file, command)"},
		{
			name: "a name used twice", file: item(`file = "a"`) + "[[done]]\nname = \"it\"\nfile = \"b\"\n",
			want: `done "it" name is used twice`,
		},
		{name: "a path that escapes", file: item(`file = "../secret"`), want: "want a path inside the project"},
		{name: "a nested escape", file: item(`file = "docs/../../secret"`), want: "want a path inside the project"},
		{name: "an absolute path", file: item(`file = "/etc/passwd"`), want: "want a path inside the project"},
		{
			name: "a grep file that escapes", file: item(`grep = { file = "../x", pattern = "a" }`),
			want: `done "it" grep.file is "../x"`,
		},
		{
			name: "a glob that escapes", file: item(`absent = { glob = "../**/*.go", pattern = "a" }`),
			want: `done "it" absent.glob is "../**/*.go"`,
		},
		{name: "a grep with no file", file: item(`grep = { pattern = "a" }`), want: "grep.file is missing"},
		{name: "a grep given a glob", file: item(`grep = { glob = "*.go", pattern = "a" }`), want: "glob is not read"},
		{
			name: "a pattern that does not compile", file: item(`grep = { file = "a", pattern = "(" }`),
			want: "grep.pattern is not a regular expression",
		},
		{name: "an empty pattern", file: item(`absent = { glob = "*.go", pattern = "" }`), want: "pattern is empty"},
		{
			name: "an unprefixed package", file: item(`test = "internal/config -run TestX"`),
			want: `names the package "internal/config", want ./internal/config`,
		},
		{name: "a package outside", file: item(`test = "./../other"`), want: "which is outside the project"},
		{name: "a test with no package", file: item(`test = "-run TestX"`), want: "names no package"},
		{name: "a test flag other than -run", file: item(`test = "./a -count=1"`), want: `has "-count=1"`},
		{name: "a package after -run", file: item(`test = "./a -run TestX ./b"`), want: `has "./b" after -run`},
		{name: "a -run that does not compile", file: item(`test = "./a -run ("`), want: "-run pattern that is not"},
		{
			name: "a timeout that does not parse", file: item("command = \"true\"\ntimeout = \"soon\""),
			want: `timeout is "soon"`,
		},
		{name: "a timeout on a cheap kind", file: item("file = \"a\"\ntimeout = \"2m\""), want: "only a test or a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := goal.Load(project(t, map[string]string{goalFile: tt.file}))

			var invalid *goal.Error

			require.ErrorAs(t, err, &invalid)
			assert.Contains(t, err.Error(), tt.want)
			assert.Equal(t, tt.line, invalid.Line)
		})
	}
}
