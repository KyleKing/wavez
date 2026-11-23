package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/goal"
)

// goalProject is a project root holding a README, with a goal when body is
// not empty.
func goalProject(t *testing.T, body string) string {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("An empty pattern is refused.\n"), 0o600))

	if body != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(root, goal.Dir), 0o750))
		require.NoError(t, os.WriteFile(goal.Path(root), []byte(body), 0o600))
	}

	return root
}

const (
	goalSentence = "goal = \"ParseGlob rejects an empty pattern\"\n"
	goalDone     = "\n[[done]]\nname = \"documented\"\ngrep = { file = \"README.md\", pattern = \"empty pattern\" }\n"
	goalUnmet    = "\n[[done]]\nname = \"written up\"\nfile = \"docs/glob.md\"\n"
	goalBuilds   = "\n[[done]]\nname = \"builds\"\ncommand = \"true\"\n"
	goalAbstains = "\n[[done]]\nname = \"lints clean\"\ncommand = \"exit 127\"\n"
)

func TestGoal_Status(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		out  string
		args []string
		code int
	}{
		{name: "no goal is not a failure", out: "no goal is set: start one with `wavez goal init \"<sentence>\"`\n"},
		{name: "no goal as JSON", args: []string{"status", "--json"}, out: "{}\n"},
		{
			name: "all done exits 0", file: goalSentence + goalDone + goalBuilds, args: []string{"status"},
			out: "ParseGlob rejects an empty pattern\n[x] documented\n[x] builds\n2 of 2 done\n",
		},
		{
			name: "something not done exits 1", file: goalSentence + goalDone + goalUnmet + goalAbstains, code: 1,
			out: "ParseGlob rejects an empty pattern\n[x] documented\n" +
				"[ ] written up — docs/glob.md does not exist\n" +
				"[?] lints clean — could not check: the shell could not run it (exit 127)\n" +
				"1 of 3 done, 1 could not be checked\n",
		},
		{
			name: "only what could not be checked is left", file: goalSentence + goalDone + goalAbstains,
			out: "ParseGlob rejects an empty pattern\n[x] documented\n" +
				"[?] lints clean — could not check: the shell could not run it (exit 127)\n" +
				"1 of 2 done, 1 could not be checked\n" +
				"nothing is known to be unmet: a condition that could not be checked is not a failure\n",
		},
		{
			name: "no conditions yet", file: goalSentence,
			out: "ParseGlob rejects an empty pattern\nno conditions yet: add one with `wavez goal add`\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := goalProject(t, tt.file)

			code, out, errOut := invoke(t, "", append([]string{"goal", "--dir", root}, tt.args...)...)
			assert.Equal(t, tt.code, code, errOut)
			assert.Equal(t, tt.out, out)
			assert.Empty(t, errOut)
		})
	}
}

func TestGoal_StatusJSON(t *testing.T) {
	t.Parallel()

	root := goalProject(t, goalSentence+"block = true\n"+goalDone+goalUnmet+goalAbstains)

	code, out, errOut := invoke(t, "", "goal", "status", "--json", "--dir", root)
	require.Equal(t, 1, code, errOut)

	var got struct {
		Goal    string        `json:"goal"`
		Items   []goal.Result `json:"items"`
		Summary goal.Summary  `json:"summary"`
		Block   bool          `json:"block"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "ParseGlob rejects an empty pattern", got.Goal)
	assert.True(t, got.Block)
	assert.Equal(t, []goal.Result{
		{Name: "documented", Kind: goal.KindGrep, Status: goal.StatusDone},
		{Name: "written up", Kind: goal.KindFile, Status: goal.StatusNotDone, Detail: "docs/glob.md does not exist"},
		{
			Name: "lints clean", Kind: goal.KindCommand, Status: goal.StatusCouldNotCheck,
			Detail: "the shell could not run it (exit 127)",
		},
	}, got.Items)
	assert.Equal(t, goal.Summary{
		Goal: "ParseGlob rejects an empty pattern", Unmet: []string{"written up"}, Total: 3, Done: 1, CouldNotCheck: 1,
	}, got.Summary)
}

func TestGoal_InvalidFileIsAnErrorWithItsLine(t *testing.T) {
	t.Parallel()

	root := goalProject(t, goalSentence+"blocks = true\n")

	code, out, errOut := invoke(t, "", "goal", "--dir", root)
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "goal.toml:2: blocks is not a known key")
}

func TestGoal_InitAddClear(t *testing.T) {
	t.Parallel()

	root := goalProject(t, "")

	steps := []struct {
		out  string
		args []string
		code int
	}{
		{args: []string{"init", "ParseGlob rejects an empty pattern", "--block"}, out: "wrote .wavez/goal.toml\n"},
		{args: []string{"init", "again"}, code: 1},
		{
			args: []string{"add", "grep", "README.md", "empty pattern", "--name", "documented"},
			out:  `added grep "documented"`,
		},
		{args: []string{"add", "--name", "written up", "file", "docs/glob.md"}, out: "added file \"written up\"\n"},
		{args: []string{"add", "command", "--timeout", "30s", "--", "test", "-f", "README.md"}, out: "added command"},
		{args: []string{"add", "absent", "*.md", `TODO\(glob\)`}, out: "added absent"},
		{args: []string{"add", "test", "internal/config -run TestX"}, code: 1},
		{args: []string{"add", "grep", "README.md"}, code: 1},
		{args: []string{"add", "file", "../outside"}, code: 1},
		{args: []string{"status"}, code: 1, out: "[x] documented\n[ ] written up — docs/glob.md does not exist\n" +
			"[x] command test -f README.md\n[x] absent *.md TODO\\(glob\\)\n3 of 4 done\n"},
		{args: []string{"clear"}, out: "cleared the goal\n"},
		{args: []string{"clear"}, out: "no goal was set\n"},
	}

	for _, step := range steps {
		code, out, errOut := invoke(t, "", append([]string{"goal", "--dir", root}, step.args...)...)
		assert.Equal(t, step.code, code, "%v: %s", step.args, errOut)
		assert.Contains(t, out, step.out, "%v", step.args)

		if step.code != 0 && step.out == "" {
			assert.Empty(t, out, "%v", step.args)
			assert.NotEmpty(t, errOut, "%v", step.args)
		}
	}

	assert.NoFileExists(t, goal.Path(root))
	assert.NoFileExists(t, goal.StatePath(root))
}

func TestGoal_Usage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "an unknown subcommand", args: []string{"finish"}},
		{name: "an unknown flag", args: []string{"status", "--nope"}},
		{name: "init with no sentence", args: []string{"init"}},
		{name: "add with no kind", args: []string{"add"}},
		{name: "status with a stray word", args: []string{"status", "now"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := goalProject(t, goalSentence)

			code, out, errOut := invoke(t, "", append([]string{"goal", "--dir", root}, tt.args...)...)
			assert.Equal(t, 1, code)
			assert.Empty(t, out)
			assert.Contains(t, errOut, "usage:\n  wavez goal [status] [--json]")
		})
	}
}
