package hook_test

import (
	"fmt"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/hook"
)

const calcSource = `package calc

// Counter counts.
type Counter struct{ n int }

// Value is the count.
func (c *Counter) Value() int { return c.n }

// Inc adds one.
func (c *Counter) Inc() { c.n++ }

// Foo is a function.
func Foo() int { return 1 }
`

// calcModule is a module with one package the rename cases aim at and the
// files around it that only look like Go.
func calcModule(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "calc/calc.go", calcSource)
	write(t, root, "calc/use.go", "package calc\n\nfunc use(c *Counter) int { c.Inc(); return c.Value() + Foo() }\n")
	write(t, root, "calc/calc.go.bak", calcSource)
	write(t, root, "calc/page.gohtml", "{{ Foo }}\n")
	write(t, root, "calc/notes.go.md", "Foo is a function.\n")
	write(t, root, "README.md", "Foo, see https://pkg.go.dev/example.com/m\n")
	write(t, root, "docs/guide.md", "Call Foo.\n")
	write(t, root, "web/app.ts", "export function Foo() {}\n")
	write(t, root, "server/handler.go",
		"package server\n\nimport \"context\"\n\n// Handle handles.\nfunc Handle(ctx context.Context) { _ = ctx }\n")
	write(t, root, "server/server.go",
		"package server\n\nimport \"context\"\n\n// Server serves.\ntype Server struct{ ctx context.Context }\n")
	write(t, root, "other/thing.go", "package other\n\n// Foo is another package's Foo.\nfunc Foo() {}\n")
	write(t, root, "cmd/app/main.go", "package main\n\nfunc init() {}\n\nfunc main() {}\n")

	return root
}

func TestHandle_PreToolUse_DoesNotDenyWhatIsNotARename(t *testing.T) {
	t.Parallel()

	elsewhere := t.TempDir()
	write(t, elsewhere, "go.mod", "module example.com/elsewhere\n")
	write(t, elsewhere, "calc.go", calcSource)

	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "a sed line inside a heredoc body is text, not a command",
			command: "cat > /tmp/note.txt <<'X'\nsed -i 's/Foo/Bar/g' calc/calc.go\nX",
		},
		{
			name:    "a heredoc with a dash and an unquoted delimiter",
			command: "cat <<-EOF > notes.txt\n\tsed -i 's/Foo/Bar/g' calc/calc.go\n\tEOF\necho done",
		},
		{name: "a file outside the project", command: `sed -i 's/Foo/Bar/g' ` + filepath.Join(elsewhere, "calc.go")},
		{name: "a backup of a Go file", command: `sed -i 's/Foo/Bar/g' calc/calc.go.bak`},
		{name: "a template whose extension starts with go", command: `sed -i 's/Foo/Bar/g' calc/page.gohtml`},
		{name: "a markdown file with .go in its name", command: `sed -i 's/Foo/Bar/g' calc/notes.go.md`},
		{
			name:    "switching a call to a method that already exists",
			command: `sed -i 's/Value/Inc/' calc/use.go`,
		},
		{name: "main, which wavez rename refuses", command: `sed -i 's/\bmain\b/run/' cmd/app/main.go`},
		{name: "init, which wavez rename refuses", command: `sed -i 's/\binit\b/setup/' cmd/app/main.go`},
		{name: "ripgrep over another language", command: `rg -l -t ts Foo web | xargs sed -i s/Foo/Bar/g`},
		{name: "git grep over docs", command: `git grep -l Foo -- docs | xargs sed -i s/Foo/Bar/g`},
		{name: "fd over markdown", command: `fd -e md | xargs sed -i s/Foo/Bar/g`},
		{name: "a pipeline with no file filter at all", command: `grep -rl Foo . | xargs sed -i s/Foo/Bar/g`},
		{
			name:    "a local variable that shares its name with a field the file never selects",
			command: `sed -i 's/\bctx\b/c/g' server/handler.go`,
		},
		{
			name:    "a Go file named only in a trailing comment",
			command: `sed -i s/Foo/Bar/g README.md # see calc/calc.go`,
		},
		{
			name:    "a URL that holds .go is not a file filter",
			command: `curl -s https://pkg.go.dev/example.com/m | xargs sed -i s/Foo/Bar/g`,
		},
		{
			name:    "perl -M holds an i and is not -i",
			command: `perl -MFile::Slurp -pe 's/Foo/Bar/g' calc/calc.go`,
		},
		{name: "a flag the reader does not know", command: `sed -i --posix -Q 's/Foo/Bar/g' calc/calc.go`},
		{name: "find -exec with only a terminator", command: `find . -type f -exec \;`},
		{name: "find -exec with only a plus", command: `find x -exec +`},
		{
			name:    "a Go file whose package does not declare the name",
			command: `sed -i 's/Foo/Bar/g' server/handler.go`,
		},
		{
			name:    "a Go file that does not exist",
			command: `sed -i 's/Foo/Bar/g' calc/gone.go`,
		},
		{name: "after a cd out of the project", command: `cd /tmp && sed -i 's/Foo/Bar/g' calc/calc.go`},
		{name: "after a cd nobody can follow", command: `cd "$DIR" && sed -i 's/Foo/Bar/g' calc/calc.go`},
		{name: "a glob that matches no Go file", command: `sed -i 's/Foo/Bar/g' docs/*`},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := calcModule(t)

			res, err := fire(t, hook.New(&fakeRunner{}, hook.WithLookPath(found)),
				shell(root, fmt.Sprintf("fp-%d", i), tt.command))
			require.NoError(t, err)
			assert.Empty(t, res.raw, "a wrong denial costs the agent a turn")
			assert.NoDirExists(t, filepath.Join(root, ".wavez"), "and nothing was recorded")
		})
	}
}

func TestHandle_PreToolUse_DeniesARenameHoweverItIsWrapped(t *testing.T) {
	t.Parallel()

	const foo = "  wavez rename calc/calc.go:Foo Bar"

	tests := []struct {
		name    string
		command string
		cwd     string
		want    string
	}{
		{name: "through bash -c", command: `bash -c "sed -i 's/Foo/Bar/g' calc/calc.go"`, want: foo},
		{name: "through sh -lc", command: `sh -lc 'sed -i s/Foo/Bar/g calc/calc.go'`, want: foo},
		{name: "a glob that takes in the package's Go files", command: `sed -i 's/Foo/Bar/g' calc/*`, want: foo},
		{name: "after a cd into the package", command: `cd calc && sed -i 's/Foo/Bar/g' calc.go`, want: foo},
		{name: "from a cwd inside the package", command: `sed -i 's/Foo/Bar/g' calc.go`, cwd: "calc", want: foo},
		{name: "under a comment line", command: "# rename it\nsed -i 's/Foo/Bar/g' calc/calc.go", want: foo},
		{
			name:    "after a heredoc has ended",
			command: "cat > n.txt <<EOF\nhello\nEOF\nsed -i 's/Foo/Bar/g' calc/calc.go",
			want:    foo,
		},
		{
			name:    "a file that only uses the name, in the package that declares it",
			command: `sed -i 's/Foo/Bar/g' calc/use.go`, want: foo,
		},
		{
			name:    "a method, through the file that calls it",
			command: `sed -i 's/Value/Count/g' calc/use.go`,
			want:    "  wavez rename calc/calc.go:Counter.Value Count",
		},
		{
			name:    "a name two packages declare offers the one the file is in",
			command: `sed -i 's/Foo/Bar/g' other/thing.go`,
			want:    "  wavez rename other/thing.go:Foo Bar",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := calcModule(t)

			res, err := fire(t, hook.New(&fakeRunner{}, hook.WithLookPath(found), hook.WithProjectDir(root)),
				shell(filepath.Join(root, tt.cwd), fmt.Sprintf("tp-%d", i), tt.command))
			require.NoError(t, err)
			require.Equal(t, "deny", res.Specific.PermissionDecision, res.raw)

			lines := strings.Split(res.Specific.PermissionReason, "\n")
			assert.Equal(t, []string{tt.want}, lines[1:len(lines)-1], "one target, in the package the command touches")
		})
	}
}

func TestHandle_PreToolUse_WithoutGoplsNothingIsDenied(t *testing.T) {
	t.Parallel()

	root := calcModule(t)

	res, err := fire(t, hook.New(&fakeRunner{}, hook.WithLookPath(missing)),
		shell(root, "no-gopls", `sed -i 's/Foo/Bar/g' calc/calc.go`))
	require.NoError(t, err)
	assert.Empty(t, res.raw, "the command it would suggest cannot run without gopls")
}

func FuzzTextRenames(f *testing.F) {
	for _, seed := range []string{
		`find . -type f -exec \;`,
		`find x -exec +`,
		`find . -name '*.go' -exec sed -i 's/Read/ReadLog/' {} \;`,
		`sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`,
		`sed -i '' -e 's/Read/ReadLog/g' a.go`,
		`perl -pi -e 's/\bRead\b/ReadLog/g' $(git grep -l Read -- '*.go')`,
		`perl -MFile::Slurp -pe 's/A/B/' x.go`,
		`gofmt -r 'bench.Read -> bench.ReadLog' -w .`,
		`bash -c "sed -i 's/Foo/Bar/g' calc/calc.go"`,
		`sh -c 'sh -c "sh -c \"sed -i s/A/B/ a.go\""'`,
		"cat > /tmp/n <<'X'\nsed -i 's/Foo/Bar/g' calc.go\nX",
		`cd a && cd .. && sed -i s/A/B/ a.go # trailing`,
		`grep -rl Read --include='*.go' . | xargs -n 10 -r sed -i 's/Read/ReadLog/g'`,
		`sed -i 's/Read/ReadLog/g a.go`,
		`sed -i -e`, `xargs`, `find . -exec`, `sed -`, `sed -i s`, `$(`, `'`, `\`, "", "<<", "<<-\n",
		`sed -i 's|a|b|;s#c#d#' -- *.go`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, command string) {
		for _, pair := range hook.RenamesIn(command) {
			before, after, ok := strings.Cut(pair, " ")
			if !ok || !token.IsIdentifier(before) || !token.IsIdentifier(after) || before == after {
				t.Fatalf("%q read as the rename %q, which is not one identifier to another", command, pair)
			}
		}
	})
}
