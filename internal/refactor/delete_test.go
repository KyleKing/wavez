package refactor_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

const (
	soloSource = "package lib\n\n// Solo is covered by one test.\nfunc Solo() string { return \"solo\" }\n\n" +
		"// Duo stays.\nfunc Duo() string { return \"duo\" }\n"
	soloTestSource = "package lib_test\n\nimport (\n\t\"strings\"\n\t\"testing\"\n\n\t\"example.com/m/lib\"\n)\n\n" +
		"func TestSolo(t *testing.T) {\n\tif !strings.HasPrefix(lib.Solo(), \"so\") {\n" +
		"\t\tt.Fatal(\"Solo\")\n\t}\n}\n\n" +
		"func TestDuo(t *testing.T) {\n\tif lib.Duo() != \"duo\" {\n\t\tt.Fatal(\"Duo\")\n\t}\n}\n"
)

func TestDelete(t *testing.T) {
	t.Parallel()
	needGopls(t)

	solo := map[string]string{"lib/solo.go": soloSource, "lib/solo_test.go": soloTestSource}

	cases := []struct {
		extra map[string]string
		// gone maps a file to text it must no longer hold, and kept to text
		// it must still hold.
		gone    map[string]string
		kept    map[string]string
		name    string
		targets []string
		want    refactor.DeleteResult
	}{
		{
			name: "a function and its test in one call", extra: solo,
			targets: []string{"lib/solo.go:Solo", "lib/solo_test.go:TestSolo"},
			want: refactor.DeleteResult{
				Count:   2,
				Deleted: []string{"lib/solo.go (Solo)", "lib/solo_test.go (TestSolo)"},
			},
			gone: map[string]string{"lib/solo.go": "Solo", "lib/solo_test.go": "strings"},
			kept: map[string]string{
				"lib/solo.go":      "package lib\n\n// Duo stays.\nfunc Duo() string { return \"duo\" }\n",
				"lib/solo_test.go": "import (\n\t\"testing\"\n\n\t\"example.com/m/lib\"\n)\n\nfunc TestDuo(",
			},
		},
		{
			name: "everything in a file, which removes it", extra: solo,
			targets: []string{"lib/solo_test.go:TestSolo,TestDuo"},
			want: refactor.DeleteResult{
				Count:   2,
				Deleted: []string{"lib/solo_test.go (TestSolo, TestDuo)"},
				Removed: []string{"lib/solo_test.go"},
			},
		},
		{
			name:    "a type, which takes its methods",
			targets: []string{"lib/lib.go:Lid"},
			want:    refactor.DeleteResult{Count: 2, Deleted: []string{"lib/lib.go (Lid, Lid.Run)"}},
			gone:    map[string]string{"lib/lib.go": "Lid"},
			kept: map[string]string{
				"lib/lib.go": "// Run is declared on two types.\nfunc (*Box) Run() {}\n\n// Limits",
			},
		},
		{
			name:    "a const block with the function that used it",
			targets: []string{"lib/lib.go:unused,MinSize,MaxSize"},
			want:    refactor.DeleteResult{Count: 3, Deleted: []string{"lib/lib.go (MinSize, MaxSize, unused)"}},
			gone:    map[string]string{"lib/lib.go": "MinSize"},
			kept:    map[string]string{"lib/lib.go": "func (*Box) Run() {}\n"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, tc.extra)

			res, err := newRefactorer(t, root).Delete(t.Context(), tc.targets)
			require.NoError(t, err)
			assert.Equal(t, tc.want, res)
			assert.Contains(t, res.Summary(), "declarations: "+tc.want.Deleted[0])

			for _, file := range tc.want.Removed {
				assert.NoFileExists(t, filepath.Join(root, file))
				assert.Contains(t, res.Summary(), "removed "+file)
			}

			for file, text := range tc.gone {
				assert.NotContains(t, read(t, root, file), text)
			}

			for file, text := range tc.kept {
				assert.Contains(t, read(t, root, file), text)
			}

			builds(t, root)
		})
	}
}

func TestDelete_Refuses(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		check   func(t *testing.T, err error)
		name    string
		wantMsg string
		targets []string
	}{
		{
			name: "a function its test and another package still use", targets: []string{"lib/lib.go:Greet"},
			check: func(t *testing.T, err error) {
				t.Helper()

				var used *refactor.StillUsedError

				require.ErrorAs(t, err, &used)
				assert.Equal(t, []refactor.Holder{
					{Name: "Greet", File: "app/app.go", In: "Hello", Line: 7},
					{Name: "Greet", File: "lib/lib_test.go", In: "TestGreet", Line: 11},
				}, used.Holders)
				assert.Equal(t,
					"wavez delete app/app.go:Hello lib/lib.go:Greet lib/lib_test.go:TestGreet", used.Retry)
			},
			wantMsg: "  Greet used at lib/lib_test.go:11 in TestGreet",
		},
		{
			name: "a helper used by a function that stays", targets: []string{"lib/lib.go:shout"},
			check:   isType[*refactor.StillUsedError],
			wantMsg: "shout used at lib/lib.go:11 in Greet",
		},
		{
			name: "a type used by a var and a test", targets: []string{"lib/lib.go:Box"},
			check:   isType[*refactor.StillUsedError],
			wantMsg: "Box used at lib/lib_test.go:17 in TestBox",
		},
		{
			name: "one name of a const block", targets: []string{"lib/lib.go:MaxSize"},
			check:   isType[*refactor.GroupedError],
			wantMsg: "MaxSize is declared in one block with MinSize",
		},
		{
			name:    "a method an interface assertion needs, caught by the build",
			targets: []string{"lib/lib.go:Box.String"},
			check: func(t *testing.T, err error) {
				t.Helper()

				var build *refactor.BuildError

				require.ErrorAs(t, err, &build)
				assert.False(t, build.Preexisting)
			},
			wantMsg: "missing method String",
		},
		{
			name: "a name that is not there", targets: []string{"lib/lib.go:Greet", "lib/lib_test.go:TestGreat"},
			check:   isType[*refactor.NotFoundError],
			wantMsg: "closest names there: TestGreet",
		},
		{
			name: "a malformed target", targets: []string{"lib/lib.go"},
			check: isType[*refactor.TargetError], wantMsg: "want path/to/file.go:Name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, nil)
			before := contents(t, root)

			_, err := newRefactorer(t, root).Delete(t.Context(), tc.targets)
			require.Error(t, err)
			tc.check(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Equal(t, before, contents(t, root), "a refused delete must leave the tree alone")
		})
	}
}

func TestDelete_GoplsMissing(t *testing.T) {
	t.Parallel()

	root := fixture(t, nil)
	before := contents(t, root)

	_, err := newRefactorer(t, root, refactor.WithGopls("gopls-is-not-installed")).
		Delete(t.Context(), []string{"lib/lib.go:unused"})

	var missing *refactor.ToolMissingError

	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "gopls-is-not-installed", missing.Program)
	assert.Equal(t, before, contents(t, root))
}
