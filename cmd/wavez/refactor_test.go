package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refactorModule writes a two-file module into a temp directory. The tests
// here cover the command line; the refactors themselves are covered in
// internal/refactor.
func refactorModule(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not on PATH")
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	files := map[string]string{
		"go.mod": "module example.com/cli\n\ngo 1.24\n",
		"a.go": "package cli\n\n// Old is renamed.\nfunc Old() int { return helper() }\n\n" +
			"func helper() int { return 1 }\n",
		"a_test.go": "package cli\n\nimport \"testing\"\n\n" +
			"func TestOld(t *testing.T) {\n\tif Old() != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o600))
	}

	return root
}

func TestRefactorCommands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		wantOut  string
		wantErr  string
		args     []string
		wantCode int
	}{
		{
			name:    "rename with --root first",
			args:    []string{"rename", "--root", "ROOT", "a.go:Old", "New"},
			wantOut: "renamed Old to New in 2 files: a.go a_test.go\n",
		},
		{
			name:    "rename with --root=DIR last",
			args:    []string{"rename", "a.go:Old", "New", "--root=ROOT"},
			wantOut: "renamed Old to New in 2 files: a.go a_test.go\n",
		},
		{
			name:    "move",
			args:    []string{"move", "-root", "ROOT", "a.go:helper", "b.go"},
			wantOut: "moved helper from a.go to b.go (created)\n",
		},
		{
			name: "delete a function with its test",
			args: []string{"delete", "--root", "ROOT", "a.go:Old", "a_test.go:TestOld"},
			wantOut: "deleted 2 declarations: a.go (Old); a_test.go (TestOld)\n" +
				"removed a_test.go, left with nothing in it\n",
		},
		{
			name:     "delete refused",
			args:     []string{"delete", "--root", "ROOT", "a.go:Old"},
			wantErr:  "wavez delete: still used outside the declarations being deleted",
			wantCode: 1,
		},
		{
			name:     "rename with a missing argument",
			args:     []string{"rename", "--root", "ROOT", "a.go:Old"},
			wantErr:  "wavez rename: got 1 arguments\n" + renameUsage,
			wantCode: 1,
		},
		{
			name:     "move with an unknown flag",
			args:     []string{"move", "--force", "a.go:Old", "b.go"},
			wantErr:  "unknown or incomplete flag --force\n" + moveUsage,
			wantCode: 1,
		},
		{
			name:     "delete with nothing to delete",
			args:     []string{"delete"},
			wantErr:  deleteUsage,
			wantCode: 1,
		},
		{
			name:     "a root that does not exist",
			args:     []string{"rename", "--root", "ROOT/absent", "a.go:Old", "New"},
			wantErr:  "wavez rename: opening the module:",
			wantCode: 1,
		},
		{name: "rename help", args: []string{"rename", "--help"}, wantOut: renameUsage + "\n"},
		{name: "move help", args: []string{"move", "-h"}, wantOut: moveUsage + "\n"},
		{name: "delete help", args: []string{"delete", "--help"}, wantOut: deleteUsage + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := refactorModule(t)
			args := make([]string, 0, len(tc.args))

			for _, arg := range tc.args {
				args = append(args, filepath.FromSlash(replaceRoot(arg, root)))
			}

			var out, errOut bytes.Buffer

			code := run(t.Context(), args, stdio{in: &bytes.Buffer{}, out: &out, err: &errOut})

			assert.Equal(t, tc.wantCode, code, errOut.String())
			assert.Equal(t, tc.wantOut, out.String())

			if tc.wantErr == "" {
				assert.Empty(t, errOut.String())
			} else {
				assert.Contains(t, errOut.String(), tc.wantErr)
			}
		})
	}
}

func replaceRoot(arg, root string) string {
	return string(bytes.ReplaceAll([]byte(arg), []byte("ROOT"), []byte(root)))
}
