package refactor_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

// brokenSource is a compile error that has nothing to do with any name the
// tests refactor.
const (
	brokenSource = "package lib\n\nvar broken int = \"text\"\n"
	brokenLine   = "the touched packages already failed to build: 1 compile error before and after, " +
		"none from this change\n"
	mainSource = "package main\n\nfunc main() { helper() }\n\nfunc helper() {}\n"
)

func TestRename_KeepsARenameInAPackageThatAlreadyFailedToBuild(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{"lib/broken.go": brokenSource})

	res, err := newRefactorer(t, root).Rename(t.Context(), "lib/lib.go:Greet", "Welcome")
	require.NoError(t, err)

	assert.Equal(t, 1, res.StillBroken)
	assert.Equal(t, []string{"app/app.go", "lib/lib.go", "lib/lib_test.go"}, res.Files)
	assert.Contains(t, res.Summary(), brokenLine)
	assert.Contains(t, read(t, root, "lib/lib.go"), "func Welcome(name string) string")
	assert.Contains(t, read(t, root, "app/app.go"), `lib.Welcome("app")`)
	assert.Equal(t, brokenSource, read(t, root, "lib/broken.go"))

	// With the earlier error gone, the renamed tree is whole.
	require.NoError(t, os.Remove(filepath.Join(root, "lib", "broken.go")))
	builds(t, root)
}

// fakeGopls writes a program that answers a rename by rewriting one
// declaration and none of its callers, which is the broken result the
// build check exists to undo.
func fakeGopls(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake gopls is a shell script")
	}

	script := "#!/bin/sh\nsed 's/^func shout(/func yell(/' lib/lib.go > lib/lib.go.new &&\n" +
		"cat lib/lib.go.new > lib/lib.go && rm lib/lib.go.new && echo \"$PWD/lib/lib.go\"\n"
	path := filepath.Join(t.TempDir(), "gopls")

	require.NoError(t, os.WriteFile(path, []byte(script), 0o700)) //nolint:gosec // a test script has to be executable

	return path
}

func TestRename_RollsBackWhenItAddsAnError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		extra map[string]string
		name  string
	}{
		{name: "to a tree that built"},
		{name: "to a package that already failed to build", extra: map[string]string{"lib/broken.go": brokenSource}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, tc.extra)
			before := contents(t, root)

			_, err := newRefactorer(t, root, refactor.WithGopls(fakeGopls(t))).
				Rename(t.Context(), "lib/lib.go:shout", "yell")

			var build *refactor.BuildError

			require.ErrorAs(t, err, &build)
			assert.Contains(t, build.Output, "lib/lib.go:11:20: undefined: shout")
			assert.NotContains(t, build.Output, "broken.go", "only the errors the rename added are its to report")
			assert.Equal(t, before, contents(t, root), "a rolled-back rename must leave every byte as it was")
		})
	}
}

func TestMove_KeepsAMoveInAPackageThatAlreadyFailedToBuild(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{"lib/broken.go": brokenSource})

	res, err := newRefactorer(t, root).Move(t.Context(), "lib/lib.go:shout", "lib/text.go")
	require.NoError(t, err)

	assert.Equal(t, 1, res.StillBroken)
	assert.Contains(t, res.Summary(), brokenLine)
	assert.Contains(t, read(t, root, "lib/text.go"), "func shout(s string) string")
	assert.NotContains(t, read(t, root, "lib/lib.go"), "func shout(")
}

func TestMove_RollsBackWhenItAddsAnErrorToABrokenPackage(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{"lib/broken.go": brokenSource})
	before := contents(t, root)

	_, err := newRefactorer(t, root).Move(t.Context(), "lib/lib.go:shout", "lib/hidden_test.go")

	var build *refactor.BuildError

	require.ErrorAs(t, err, &build)
	assert.Contains(t, build.Output, "undefined: shout")
	assert.NotContains(t, build.Output, "broken.go")
	assert.Equal(t, before, contents(t, root))
}

func TestDelete_KeepsADeleteInAPackageThatAlreadyFailedToBuild(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{"lib/broken.go": brokenSource})

	res, err := newRefactorer(t, root).Delete(t.Context(), []string{"lib/lib.go:Taken"})
	require.NoError(t, err)

	assert.Equal(t, 1, res.StillBroken)
	assert.Contains(t, res.Summary(), brokenLine)
	assert.NotContains(t, read(t, root, "lib/lib.go"), "func Taken()")
}

func TestDelete_RefusesToLeavePackageMainWithoutMain(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{"cmd/tool/main.go": mainSource})
	before := contents(t, root)

	_, err := newRefactorer(t, root).Delete(t.Context(), []string{"cmd/tool/main.go:main"})

	var build *refactor.BuildError

	require.ErrorAs(t, err, &build)
	assert.Contains(t, build.Output, "cmd/tool: function main is undeclared in the main package")
	assert.Equal(t, before, contents(t, root))
}

func TestMove_KeepsMainWhenItMovesToAnotherFile(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := fixture(t, map[string]string{"cmd/tool/main.go": mainSource})

	_, err := newRefactorer(t, root).Move(t.Context(), "cmd/tool/main.go:main", "cmd/tool/entry.go")
	require.NoError(t, err)
	assert.Contains(t, read(t, root, "cmd/tool/entry.go"), "func main()")
}

func TestDryRun_ChangesNothing(t *testing.T) {
	t.Parallel()
	needGopls(t)

	const header = "dry run, nothing was changed; a real run prints:\n"

	cases := []struct {
		run  func(t *testing.T, r *refactor.Refactorer) string
		name string
		want string
	}{
		{
			name: "rename",
			run: func(t *testing.T, r *refactor.Refactorer) string {
				t.Helper()

				res, err := r.Rename(t.Context(), "lib/lib.go:Greet", "Welcome")
				require.NoError(t, err)

				return res.Summary()
			},
			want: header + "renamed Greet to Welcome in 3 files: " +
				"app/app.go (1 line) lib/lib.go (2 lines) lib/lib_test.go (1 line)\n",
		},
		{
			name: "move",
			run: func(t *testing.T, r *refactor.Refactorer) string {
				t.Helper()

				res, err := r.Move(t.Context(), "lib/lib.go:shout", "lib/text.go")
				require.NoError(t, err)

				return res.Summary()
			},
			want: header + "moved shout from lib/lib.go to lib/text.go (created)\n",
		},
		{
			name: "delete",
			run: func(t *testing.T, r *refactor.Refactorer) string {
				t.Helper()

				res, err := r.Delete(t.Context(), []string{"lib/lib.go:Taken"})
				require.NoError(t, err)

				return res.Summary()
			},
			want: header + "deleted 1 declaration: lib/lib.go (Taken)\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, nil)
			before := contents(t, root)

			summary := tc.run(t, newRefactorer(t, root, refactor.WithDryRun(true)))

			assert.Contains(t, summary, tc.want)
			assert.Equal(t, before, contents(t, root), "a dry run must leave every byte as it was")
		})
	}
}
