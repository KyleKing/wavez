package refactor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

const (
	shoutText = "// shout upper-cases s.\nfunc shout(s string) string { return strings.ToUpper(s) } // loud\n"
	osFile    = "package app\n\nimport \"os\"\n\n// Dir is the working directory.\nfunc Dir() string {\n" +
		"\tdir, _ := os.Getwd()\n\n\treturn dir\n}\n"
)

func TestMove(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		extra    map[string]string
		name     string
		target   string
		dest     string
		wantDest string
		// wantSource lists text the source must still hold, and notSource
		// text it must have lost.
		wantSource []string
		notSource  []string
		want       refactor.MoveResult
	}{
		{
			name:   "into a new file, taking its import and trailing comment",
			target: "lib/lib.go:shout", dest: "lib/text.go",
			want:       refactor.MoveResult{From: "lib/lib.go", To: "lib/text.go", Names: "shout", Created: true},
			wantDest:   "package lib\n\nimport (\n\t\"strings\"\n)\n\n" + shoutText,
			wantSource: []string{"import (\n\t\"fmt\"\n)\n", "func Taken() {}"},
			notSource:  []string{"shout upper-cases", "// loud", "\"strings\"", "\n\n\n"},
		},
		{
			name:   "into an existing file with a single import, growing a local group",
			extra:  map[string]string{"app/dir.go": osFile},
			target: "app/app.go:Hello", dest: "app/dir.go",
			want: refactor.MoveResult{From: "app/app.go", To: "app/dir.go", Names: "Hello"},
			wantDest: "package app\n\nimport (\n\t\"os\"\n\n\t\"example.com/m/lib\"\n)\n\n" +
				"// Dir is the working directory.\nfunc Dir() string {\n\tdir, _ := os.Getwd()\n\n\treturn dir\n}\n\n" +
				"// Hello calls lib.Greet, not Greeting.\nfunc Hello() string { return lib.Greet(\"app\") }\n",
			wantSource: []string{"// Package app uses lib.\npackage app\n"},
			notSource:  []string{"import", "Hello"},
		},
		{
			name:   "a method and a whole const block into an existing import block",
			extra:  map[string]string{"lib/more.go": "package lib\n\nimport (\n\t\"os\"\n)\n\nvar _ = os.Args\n"},
			target: "lib/lib.go:Box.String,MinSize,MaxSize,unused", dest: "lib/more.go",
			want: refactor.MoveResult{
				From: "lib/lib.go", To: "lib/more.go", Names: "Box.String, MinSize, MaxSize, unused",
			},
			wantDest: "package lib\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nvar _ = os.Args\n\n" +
				"// String implements fmt.Stringer.\nfunc (b Box) String() string { return fmt.Sprint(b.Size) }\n\n" +
				"// Limits bound a box.\nconst (\n\tMinSize = 1\n\tMaxSize = 9\n)\n\n" +
				"// unused is referenced by nothing.\nfunc unused() string { return fmt.Sprint(MinSize, MaxSize) }\n",
			wantSource: []string{"var _ fmt.Stringer = Box{}\n"},
			notSource:  []string{"MinSize", "Limits bound"},
		},
		{
			name:   "the last declaration, which removes the source file",
			extra:  map[string]string{"lib/tmp.go": "package lib\n\nfunc tmp() {}\n"},
			target: "lib/tmp.go:tmp", dest: "lib/kept.go",
			want: refactor.MoveResult{
				From: "lib/tmp.go", To: "lib/kept.go", Names: "tmp", Created: true, SourceRemoved: true,
			},
			wantDest: "package lib\n\nfunc tmp() {}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, tc.extra)

			res, err := newRefactorer(t, root).Move(t.Context(), tc.target, tc.dest)
			require.NoError(t, err)
			assert.Equal(t, tc.want, res)
			assert.Equal(t, tc.wantDest, read(t, root, tc.dest))
			assert.Contains(t, res.Summary(), "moved "+tc.want.Names+" from "+tc.want.From+" to "+tc.want.To)

			if tc.want.SourceRemoved {
				assert.NoFileExists(t, filepath.Join(root, tc.want.From))
				assert.Contains(t, res.Summary(), "removed "+tc.want.From)
			} else {
				source := read(t, root, tc.want.From)
				for _, want := range tc.wantSource {
					assert.Contains(t, source, want)
				}

				for _, not := range tc.notSource {
					assert.NotContains(t, source, not)
				}
			}

			builds(t, root)
		})
	}
}

func TestMove_Refuses(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		extra   map[string]string
		check   func(t *testing.T, err error)
		name    string
		target  string
		dest    string
		wantMsg string
	}{
		{
			name: "a destination in another directory", target: "lib/lib.go:Greet", dest: "app/greet.go",
			check:   isType[*refactor.CrossPackageError],
			wantMsg: "Moving between packages is not supported",
		},
		{
			name: "a destination in the external test package", target: "lib/lib.go:Greet", dest: "lib/lib_test.go",
			check:   isType[*refactor.CrossPackageError],
			wantMsg: "the destination is package lib_test (lib/lib_test.go)",
		},
		{
			name: "one name of a const block", target: "lib/lib.go:MinSize", dest: "lib/limits.go",
			check:   isType[*refactor.GroupedError],
			wantMsg: "MinSize is declared in one block with MaxSize at lib/lib.go:44",
		},
		{
			name: "the file it is already in", target: "lib/lib.go:Greet", dest: "lib/lib.go",
			check: isType[*refactor.TargetError], wantMsg: "already in lib/lib.go",
		},
		{
			name: "a destination that is not a Go file", target: "lib/lib.go:Greet", dest: "lib/notes.txt",
			check: isType[*refactor.TargetError], wantMsg: "must be a .go file",
		},
		{
			name: "a destination that is a directory", target: "lib/lib.go:Greet", dest: "lib/sub.go",
			extra: map[string]string{"lib/sub.go/keep.txt": ""},
			check: isType[*refactor.TargetError], wantMsg: "is a directory",
		},
		{
			name:   "names from two files",
			extra:  map[string]string{"lib/extra.go": "package lib\n\n// Extra is elsewhere.\nfunc Extra() {}\n"},
			target: "lib:Greet,Extra", dest: "lib/new.go",
			check: isType[*refactor.TargetError], wantMsg: "move from one file per call",
		},
		{
			name: "a name that is not there", target: "lib/lib.go:Nope", dest: "lib/new.go",
			check: isType[*refactor.NotFoundError], wantMsg: `no declaration named "Nope"`,
		},
		{
			name: "a struct field", target: "lib/lib.go:Box.Size", dest: "lib/new.go",
			check: isType[*refactor.NotFoundError], wantMsg: `no declaration named "Box.Size"`,
		},
		{
			name:   "a result that does not build, into a file it created",
			target: "lib/lib.go:shout", dest: "lib/hidden_test.go",
			check: func(t *testing.T, err error) {
				t.Helper()

				var build *refactor.BuildError

				require.ErrorAs(t, err, &build)
				assert.False(t, build.Preexisting)
			},
			wantMsg: "lib/lib.go:10:20: undefined: shout",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, tc.extra)
			before := contents(t, root)

			_, err := newRefactorer(t, root).Move(t.Context(), tc.target, tc.dest)
			require.Error(t, err)
			tc.check(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Equal(t, before, contents(t, root), "a refused move must leave the tree alone")
		})
	}
}

func TestMove_LeavesAnUnformattedSourceInItsOwnStyle(t *testing.T) {
	t.Parallel()
	needGopls(t)

	const loose = "package lib\n\nfunc keep( ) int {return 1}\n\n\n// gone leaves.\nfunc gone() {}\n"

	root := fixture(t, map[string]string{"lib/loose.go": loose})

	_, err := newRefactorer(t, root).Move(t.Context(), "lib/loose.go:gone", "lib/tidy.go")
	require.NoError(t, err)

	assert.Equal(t, "package lib\n\nfunc keep( ) int {return 1}\n", read(t, root, "lib/loose.go"))
	assert.Equal(t, "package lib\n\n// gone leaves.\nfunc gone() {}\n", read(t, root, "lib/tidy.go"))
}

func TestMove_KeepsTheFileMode(t *testing.T) {
	t.Parallel()
	needGopls(t)

	// The fixture writes its files 0600, which is not the mode a new file
	// gets, so a source that kept its mode is told from one rewritten.
	root := fixture(t, nil)

	_, err := newRefactorer(t, root).Move(t.Context(), "lib/lib.go:Taken", "lib/taken.go")
	require.NoError(t, err)

	for file, want := range map[string]os.FileMode{"lib.go": 0o600, "taken.go": 0o644} {
		info, statErr := os.Stat(filepath.Join(root, "lib", file))
		require.NoError(t, statErr)
		assert.Equal(t, want, info.Mode().Perm(), file)
	}
}
