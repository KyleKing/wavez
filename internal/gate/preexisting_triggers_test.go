package gate_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
)

// flakyTest fails the first time it runs in a given directory and passes
// every time after, by leaving a marker outside the tree under markers.
func flakyTest(markers string) string {
	return "package a\n\nimport (\n\t\"crypto/sha256\"\n\t\"fmt\"\n\t\"os\"\n" +
		"\t\"path/filepath\"\n\t\"testing\"\n)\n\n" +
		"func TestFlaky(t *testing.T) {\n\twd, _ := os.Getwd()\n" +
		"\tmarker := filepath.Join(\"" + markers + "\", fmt.Sprintf(\"%x\", sha256.Sum256([]byte(wd))))\n" +
		"\tif _, err := os.Stat(marker); err != nil {\n\t\t_ = os.WriteFile(marker, nil, 0o600)\n" +
		"\t\tt.Fatal(\"unlucky this time\")\n\t}\n}\n"
}

func TestRun_FlakyTestDoesNotBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// base is committed before the session; during is what it writes.
		base    map[string]string
		during  map[string]string
		changed []string
		status  gate.Status
		flaky   []string
		agent   []string
		hidden  []string
	}{
		{
			name:    "a committed test that fails and then passes while the session only adds a comment",
			during:  map[string]string{"a/a.go": srcA + "\n// A comment is all the session wrote.\n"},
			changed: []string{"a/a.go"}, status: gate.StatusPass,
			flaky:  []string{"example.com/scratch/a TestFlaky"},
			hidden: []string{"TestFlaky", "unlucky"},
		},
		{
			name:    "a flaky test beside one the session really broke leaves only the real one",
			during:  map[string]string{"a/a.go": srcAThree},
			changed: []string{"a/a.go"}, status: gate.StatusFail,
			flaky:  []string{"example.com/scratch/a TestFlaky"},
			agent:  []string{"--- FAIL: TestA"},
			hidden: []string{"TestFlaky", "unlucky"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			markers := t.TempDir()
			root := committed(t, module(map[string]string{"a/flaky_test.go": flakyTest(markers)}))
			ctx := began(t, root)
			apply(t, root, tt.during)

			report := runGate(ctx, t, root, gate.StageStop, tt.changed)

			got := result(t, report, "test")
			assert.Equal(t, tt.status, got.Status, "%+v", got)
			assert.Equal(t, tt.flaky, got.Flaky)
			assert.Empty(t, got.Preexisting, "it passed at the baseline the second time, so it is not an old failure")
			assert.Equal(t, tt.status == gate.StatusFail, report.Failed())
			told(t, report.Render(), tt.agent, tt.hidden)
			told(t, report.Notes(), []string{"FLAKY", "TestFlaky"}, nil)
		})
	}
}

func TestRun_BaselineTreeDoesNotFireRepositoryHooks(t *testing.T) {
	t.Parallel()

	root := committed(t, module(map[string]string{"a/a_test.go": srcATestRed}))
	fired := filepath.Join(t.TempDir(), "fired")
	writeFile(t, root, ".git/hooks/post-checkout", "#!/bin/sh\necho fired >> '"+fired+"'\n")

	ctx := began(t, root)
	writeFile(t, root, "a/extra.go", srcExtra)

	report := runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"}, gate.WithoutCache())

	assert.Equal(t, gate.StatusPreexisting, result(t, report, "test").Status)
	assert.NoFileExists(t, fired, "checking out the baseline ran post-checkout")
}

func TestRun_SweepsTheTreeAKilledRunLeft(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	root := committed(t, module(map[string]string{"a/a_test.go": srcATestRed}))

	// What SIGKILL leaves: a registered worktree whose run never came back
	// to remove it, here long enough ago that no run could still own it.
	stale := filepath.Join(tmp, "wavez-baseline-killed")
	//nolint:gosec // fixed program, test arguments
	add := exec.CommandContext(t.Context(), "git", "worktree", "add", "--detach", filepath.Join(stale, "tree"), "HEAD")
	git(t, root, add)

	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))

	recent := filepath.Join(tmp, "wavez-baseline-running")
	require.NoError(t, os.MkdirAll(recent, 0o750))

	require.Equal(t, 2, strings.Count(worktrees(t, root), "worktree "))

	ctx := began(t, root)
	writeFile(t, root, "a/extra.go", srcExtra)

	report := runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"}, gate.WithoutCache())

	assert.Equal(t, gate.StatusPreexisting, result(t, report, "test").Status)
	assert.NoDirExists(t, stale)
	assert.DirExists(t, recent, "a tree young enough to be another run's is left alone")
	assert.Equal(t, 1, strings.Count(worktrees(t, root), "worktree "), "the dead registration is pruned")
}
