package refactor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

func TestFindRoot(t *testing.T) {
	t.Parallel()

	root := fixture(t, nil)

	cases := []struct {
		name    string
		dir     string
		want    string
		wantErr bool
	}{
		{name: "the root itself", dir: root, want: root},
		{name: "a package directory below it", dir: filepath.Join(root, "lib"), want: root},
		{name: "a directory with no module above it", dir: string(filepath.Separator), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := refactor.FindRoot(tc.dir)
			if tc.wantErr {
				var rootErr *refactor.RootError

				require.ErrorAs(t, err, &rootErr)
				assert.Contains(t, err.Error(), "pass --root DIR")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNew_RejectsAMissingRoot(t *testing.T) {
	t.Parallel()

	_, err := refactor.New(filepath.Join(t.TempDir(), "absent"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSkill(t *testing.T) {
	t.Parallel()

	assert.Contains(t, refactor.Skill, "name: wavez-refactor\n")
	assert.Contains(t, refactor.Skill, "wavez rename")
	assert.Contains(t, refactor.Skill, "wavez move")
	assert.Contains(t, refactor.Skill, "wavez delete")
}

func TestSummaries(t *testing.T) {
	t.Parallel()

	mentions := make([]refactor.Mention, 0, 14)
	for line := 1; line <= 14; line++ {
		mentions = append(mentions, refactor.Mention{File: "a.go", Line: line})
	}

	files := make([]string, 0, 22)
	for range 22 {
		files = append(files, "f.go")
	}

	holders := make([]refactor.Holder, 0, 17)
	for line := 1; line <= 17; line++ {
		holders = append(holders, refactor.Holder{Name: "X", File: "a.go", Line: line})
	}

	cases := []struct {
		name string
		got  string
		want []string
	}{
		{
			name: "a rename caps its mentions and its files",
			got:  refactor.RenameResult{Old: "A", New: "B", Files: files, Mentions: mentions}.Summary(),
			want: []string{"renamed A to B in 22 files: f.go", "f.go (+2 more)\n", "  a.go:12\n  (+2 more)\n"},
		},
		{
			name: "a rename of one file with nothing left over is one line",
			got:  refactor.RenameResult{Old: "A", New: "B", Files: []string{"a.go"}}.Summary(),
			want: []string{"renamed A to B in 1 file: a.go\n"},
		},
		{
			name: "a rename caps the uses the build excludes",
			got:  refactor.RenameResult{Old: "A", New: "B", Files: files, Excluded: mentions}.Summary(),
			want: []string{"these still use A:\n  a.go:1\n", "  a.go:12\n  (+2 more)\n"},
		},
		{
			name: "a rename says when it could not look for excluded uses",
			got:  refactor.RenameResult{Old: "T.A", New: "B", Files: files, ExcludedUnchecked: true}.Summary(),
			want: []string{"go list failed, so files excluded from this build were not checked for A\n"},
		},
		{
			name: "a refused delete caps its holders and omits a missing enclosing name",
			got:  (&refactor.StillUsedError{Holders: holders}).Error(),
			want: []string{"  X used at a.go:15\n  (+2 more)"},
		},
		{
			name: "a missing go tool has no install hint",
			got:  (&refactor.ToolMissingError{Program: "go"}).Error(),
			want: []string{"go is not installed or not on PATH"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, want := range tc.want {
				assert.Contains(t, tc.got, want)
			}
		})
	}
}
