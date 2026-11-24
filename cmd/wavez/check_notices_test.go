package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCheck_SaysWhenItCheckedNothing(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH, so nothing that needs a repository ran")
	}

	tests := []struct {
		name string
		// file is named on the command line; with none, init makes the
		// project a clean repository and git is asked instead.
		file   string
		errOut string
		init   bool
	}{
		{
			name: "a named file that selects no check", file: ".github/notes.md",
			errOut: "wavez check: nothing to check: no stop-stage check is selected by the 1 changed file(s)\n",
		},
		{
			name: "a clean repository with no file named", init: true,
			errOut: "wavez check: nothing to check: no file changed in the working tree\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := gateProject(t, gateClean)
			args := []string{"--root", root}

			if tt.init {
				commit := "git init -q && git add -A && " +
					"git -c user.name=t -c user.email=t@example.com commit -qm base"

				cmd := exec.CommandContext(t.Context(), "sh", "-c", commit)
				cmd.Dir = root
				require.NoError(t, cmd.Run())
			} else {
				args = append(args, filepath.Join(root, tt.file))
			}

			out, errOut, code := checkRun(t.Context(), args...)
			assert.Equal(t, 0, code)
			assert.Empty(t, out, "stdout is what an agent reads, and a run that checked nothing has nothing for it")
			assert.Contains(t, errOut, tt.errOut)
		})
	}
}

func TestRunCheck_SaysWhenANamedFileDoesNotExist(t *testing.T) {
	t.Parallel()

	root := gateProject(t, gateClean)
	missing := filepath.Join(root, "nonexistent.go")

	out, errOut, code := checkRun(t.Context(), "--root", root, missing)

	assert.Equal(t, 0, code, "a deleted file still selects its package, which builds")
	assert.Equal(t, "wavez check: passed build, test\n", out)
	assert.Equal(t, "wavez check: "+missing+" does not exist; it is checked as a file that was deleted\n", errOut)
}
