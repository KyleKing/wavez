package refactor_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

func TestRename(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		wantIn    map[string]string
		name      string
		target    string
		to        string
		wantOld   string
		wantFiles []string
	}{
		{
			name: "exported func across two packages and a test file", target: "lib/lib.go:Greet", to: "Welcome",
			wantFiles: []string{"app/app.go", "lib/lib.go", "lib/lib_test.go"},
			wantIn: map[string]string{
				"lib/lib.go":      "func Welcome(name string) string",
				"lib/lib_test.go": `lib.Welcome("x")`,
				"app/app.go":      `lib.Welcome("app")`,
			},
			wantOld: "Greet",
		},
		{
			name: "method by qualified name", target: "lib/lib.go:Box.Grow", to: "Enlarge",
			wantFiles: []string{"lib/lib.go", "lib/lib_test.go"},
			wantIn:    map[string]string{"lib/lib.go": "func (b *Box) Enlarge()", "lib/lib_test.go": "b.Enlarge()"},
			wantOld:   "Box.Grow",
		},
		{
			name: "method by bare name through the package directory", target: "./lib:Grow", to: "Enlarge",
			wantFiles: []string{"lib/lib.go", "lib/lib_test.go"},
			wantIn:    map[string]string{"lib/lib.go": "func (b *Box) Enlarge()"},
			wantOld:   "Box.Grow",
		},
		{
			name: "struct field", target: "lib/lib.go:Box.Size", to: "Width",
			wantFiles: []string{"lib/lib.go", "lib/lib_test.go"},
			wantIn:    map[string]string{"lib/lib.go": "Width int", "lib/lib_test.go": "b.Width != 1"},
			wantOld:   "Box.Size",
		},
		{
			name: "unexported func", target: "lib:shout", to: "yell",
			wantFiles: []string{"lib/lib.go"},
			wantIn:    map[string]string{"lib/lib.go": "func yell(s string) string"},
			wantOld:   "shout",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, nil)

			res, err := newRefactorer(t, root).Rename(t.Context(), tc.target, tc.to)
			require.NoError(t, err)

			assert.Equal(t, tc.wantFiles, res.Files)
			assert.Equal(t, tc.wantOld, res.Old)
			assert.Contains(t, res.Summary(), "renamed "+tc.wantOld+" to "+tc.to)

			for file, want := range tc.wantIn {
				assert.Contains(t, read(t, root, file), want)
			}

			builds(t, root)
		})
	}
}

func TestRename_ReportsLeftoverMentions(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{
		"app/notes.go": "package app\n\n// greeting mentions Greet in a comment and Greeter as a longer word.\n" +
			"const note = \"call Greet\\n\" + `\nraw\nGreet again`\n",
	})

	res, err := newRefactorer(t, root).Rename(t.Context(), "lib/lib.go:Greet", "Welcome")
	require.NoError(t, err)

	// The changed files come first, then the rest of the module. gopls
	// rewrites the declaration's own doc comment, so lib.go has none left.
	assert.Equal(t, []refactor.Mention{
		{File: "app/app.go", Line: 6},
		{File: "lib/lib_test.go", Line: 9},
		{File: "lib/lib_test.go", Line: 12},
		{File: "app/notes.go", Line: 3},
		{File: "app/notes.go", Line: 4},
		{File: "app/notes.go", Line: 6},
	}, res.Mentions)

	summary := res.Summary()
	assert.Contains(t, summary, `"Greet" still appears in 6 comment or string lines`)
	assert.Contains(t, summary, "  app/notes.go:6\n")
	assert.Contains(t, read(t, root, "app/app.go"), "// Hello calls lib.Greet, not Greeting.")
}

func TestRename_Refuses(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []refusal{
		{
			name: "ambiguous bare method name", target: "lib/lib.go:Run", to: "Go",
			check: func(t *testing.T, err error) {
				t.Helper()

				var amb *refactor.AmbiguousError

				require.ErrorAs(t, err, &amb)
				require.Len(t, amb.Candidates, 2)
				assert.Equal(t, "lib/lib.go:Lid.Run", amb.Candidates[0].Target)
				assert.Equal(t, "lib/lib.go:Box.Run", amb.Candidates[1].Target)
			},
			wantMsg: `"Run" names 2 declarations; use one of these targets instead:`,
		},
		{
			name: "not found offers near names", target: "lib/lib.go:Greeet", to: "Welcome",
			check: func(t *testing.T, err error) {
				t.Helper()

				var missing *refactor.NotFoundError

				require.ErrorAs(t, err, &missing)
				assert.Contains(t, missing.Near, "Greet")
			},
			wantMsg: `no declaration named "Greeet" in lib/lib.go; closest names there: Greet`,
		},
		{
			name: "not found points at the sibling file", target: "lib/lib.go:Extra", to: "More",
			extra:   map[string]string{"lib/extra.go": "package lib\n\n// Extra is elsewhere.\nfunc Extra() {}\n"},
			check:   isType[*refactor.NotFoundError],
			wantMsg: "so target lib/extra.go:Extra",
		},
		{
			name: "not found with nothing close", target: "app/app.go:Zzzzzzzz", to: "More",
			check:   isType[*refactor.NotFoundError],
			wantMsg: "target the file that declares it",
		},
		{
			name: "gopls refuses a conflicting name", target: "lib/lib.go:Greet", to: "Taken",
			check:   isType[*refactor.GoplsError],
			wantMsg: "conflicts with func in same block",
		},
		{
			name: "keyword", target: "lib/lib.go:Greet", to: "func",
			check:   isType[*refactor.InvalidNameError],
			wantMsg: "keyword",
		},
		{
			name: "same name", target: "lib/lib.go:Greet", to: "Greet",
			check:   isType[*refactor.InvalidNameError],
			wantMsg: "already its name",
		},
		{
			name: "gopls is not installed", target: "lib/lib.go:Greet", to: "Welcome",
			opts:    []refactor.Option{refactor.WithGopls("gopls-is-not-installed")},
			check:   isType[*refactor.ToolMissingError],
			wantMsg: "go install golang.org/x/tools/gopls@latest",
		},
		{
			name: "package does not typecheck", target: "lib/lib.go:Greet", to: "Welcome",
			extra: map[string]string{"lib/broken.go": "package lib\n\nvar broken int = \"text\"\n"},
			check: func(t *testing.T, err error) {
				t.Helper()

				var build *refactor.BuildError

				require.ErrorAs(t, err, &build)
				assert.True(t, build.Preexisting)
			},
			wantMsg: "lib/broken.go:3:18:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestRename_RefusesABadTarget(t *testing.T) {
	t.Parallel()

	cases := []refusal{
		{
			name: "no colon", target: "lib/lib.go", to: "X",
			check: isType[*refactor.TargetError], wantMsg: "want path/to/file.go:Name",
		},
		{
			name: "missing file", target: "lib/nope.go:Greet", to: "X",
			check: isType[*refactor.TargetError], wantMsg: "no such file or directory: lib/nope.go",
		},
		{
			name: "not a go file", target: "go.mod:Greet", to: "X",
			check: isType[*refactor.TargetError], wantMsg: "is not a Go file",
		},
		{
			name: "outside the module", target: "../elsewhere.go:Greet", to: "X",
			check: isType[*refactor.TargetError], wantMsg: "outside the module root",
		},
		{
			name: "two names", target: "lib/lib.go:Greet,Taken", to: "X",
			check: isType[*refactor.TargetError], wantMsg: "exactly one name",
		},
		{
			name: "empty name", target: "lib/lib.go: , ", to: "X",
			check: isType[*refactor.TargetError], wantMsg: "no name after the colon",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// refusal is one rename that must fail with a typed error and change
// nothing.
type refusal struct {
	extra   map[string]string
	check   func(t *testing.T, err error)
	name    string
	target  string
	to      string
	wantMsg string
	opts    []refactor.Option
}

func (tc refusal) run(t *testing.T) {
	t.Helper()

	root := fixture(t, tc.extra)
	before := contents(t, root)

	_, err := newRefactorer(t, root, tc.opts...).Rename(t.Context(), tc.target, tc.to)
	require.Error(t, err)
	tc.check(t, err)
	assert.Contains(t, err.Error(), tc.wantMsg)
	assert.Equal(t, before, contents(t, root), "a refused rename must leave the tree alone")
}

// isType asserts that an error is, or wraps, the domain error type E.
func isType[E error](t *testing.T, err error) {
	t.Helper()

	var want E

	require.ErrorAs(t, err, &want)
}

func TestRename_ResolvesAgainstTheWorkDir(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, nil)
	r := newRefactorer(t, root, refactor.WithWorkDir(root+"/lib"))

	res, err := r.Rename(t.Context(), "lib.go:shout", "yell")
	require.NoError(t, err)
	assert.Equal(t, []string{"lib/lib.go"}, res.Files)

	res, err = r.Rename(t.Context(), root+"/app/app.go:Hello", "Hi")
	require.NoError(t, err)
	assert.Equal(t, []string{"app/app.go"}, res.Files)
}
