package refactor_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

// excludedFiles are files no default build holds on any machine that runs
// these tests: plan9 by file name, and build tags nothing sets. Each one
// uses names of the fixture the way a caller on that build would.
func excludedFiles() map[string]string {
	return map[string]string{
		"lib/lib_plan9.go": "package lib\n\n// Greet in a comment is a mention, not a use.\n" +
			"func plan9() string { return Greet(\"p\") }\n\n" +
			"func (b *Box) wide() { b.Grow() }\n\n" +
			"func size() int { return Box{Size: 2}.Size }\n",
		"lib/tagged_test.go": "//go:build integration\n\npackage lib_test\n\nimport (\n\t\"testing\"\n\n" +
			"\tfixture \"example.com/m/lib\"\n)\n\n" +
			"func TestTagged(t *testing.T) {\n\tvar shadow struct{ Greet int }\n\n" +
			"\t_ = shadow.Greet\n\t_ = fixture.Greet(\"t\")\n}\n",
		"app/app_plan9.go": "package app\n\nimport (\n\t\"os\"\n\n\t\"example.com/m/lib\"\n)\n\n" +
			"func plan9() string {\n\t_ = os.Args\n\n\treturn lib.Greet(\"p\")\n}\n",
		"app/twin_plan9.go": "//go:build ignore\n\npackage app\n\n// Hello is this build's Hello.\n" +
			"func Hello() string { return \"\" }\n",
		"other/other_plan9.go": "package other\n\n// Greet is another package's Greet.\n" +
			"func Greet() {}\n\nvar _ = Greet\n",
	}
}

func TestRename_ReportsUsesInFilesTheBuildExcludes(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		name    string
		target  string
		to      string
		heading string
		want    []refactor.Mention
	}{
		{
			name: "a func used bare in its package and qualified by importers", target: "lib/lib.go:Greet",
			to:      "Welcome",
			heading: "not renamed, excluded from this build (GOOS/GOARCH or build tag); these still use Greet:\n",
			want: []refactor.Mention{
				{File: "app/app_plan9.go", Line: 12},
				{File: "lib/lib_plan9.go", Line: 4},
				{File: "lib/tagged_test.go", Line: 15},
			},
		},
		{
			name: "a func another build declares again", target: "app/app.go:Hello", to: "Hi",
			heading: "these still use Hello:\n",
			want:    []refactor.Mention{{File: "app/twin_plan9.go", Line: 6}},
		},
		{
			name: "a method wherever it is selected", target: "lib/lib.go:Box.Grow", to: "Enlarge",
			heading: "these select .Grow and may mean Box.Grow:\n",
			want:    []refactor.Mention{{File: "lib/lib_plan9.go", Line: 6}},
		},
		{
			name: "a field selected and keyed", target: "lib/lib.go:Box.Size", to: "Width",
			heading: "these select .Size and may mean Box.Size:\n",
			want:    []refactor.Mention{{File: "lib/lib_plan9.go", Line: 8}},
		},
		{
			name: "a name no excluded file uses", target: "lib/lib.go:Taken", to: "Held",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, excludedFiles())
			before := contents(t, root)

			res, err := newRefactorer(t, root).Rename(t.Context(), tc.target, tc.to)
			require.NoError(t, err)

			assert.Equal(t, tc.want, res.Excluded)
			assert.False(t, res.ExcludedUnchecked)

			if tc.want == nil {
				assert.NotContains(t, res.Summary(), "excluded")
			} else {
				assert.Contains(t, res.Summary(), tc.heading+"  "+tc.want[0].File)
			}

			after := contents(t, root)
			for file := range excludedFiles() {
				assert.Equal(t, before[file], after[file], "an excluded file is reported, never edited")
			}

			builds(t, root)
		})
	}
}
