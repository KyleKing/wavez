package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFile(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // a file the test wrote
	require.NoError(t, err)

	return string(raw)
}

func TestInit(t *testing.T) {
	t.Parallel()

	dir := goProject(t)
	settings := filepath.Join(dir, ".claude", "settings.json")

	code, out, _ := invoke(t, "", "init", "--dir", dir, "--print")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "# .claude/settings.json (created)")
	assert.Contains(t, out, `"command": "wavez hook"`)
	assert.Contains(t, out, "# .claude/skills/wavez-refactor/SKILL.md (created)")
	assert.NoFileExists(t, settings, "--print writes nothing")

	code, out, _ = invoke(t, "", "init", "--dir", dir)
	require.Equal(t, 0, code)
	assert.Equal(t, "created .claude/settings.json\ncreated .claude/skills/wavez-refactor/SKILL.md\n"+
		"created .wavez/.gitignore\n", out)

	code, out, _ = invoke(t, "", "init", "--dir", dir)
	require.Equal(t, 0, code)
	assert.Equal(t, "nothing to change: the wavez hooks are already installed\n", out,
		"a second run says in one line that it had nothing to do")

	code, out, _ = invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez")
	require.Equal(t, 0, code)
	assert.Equal(t, "updated .claude/settings.json\n", out)

	raw := readFile(t, settings)
	assert.Equal(t, 5, strings.Count(raw, `"/opt/wavez/wavez hook"`))
	assert.NotContains(t, raw, `"wavez hook"`)
}

func TestInit_NonGoProjectGetsNoRefactorSkill(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	code, out, errOut := invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "created .claude/settings.json\ncreated .wavez/.gitignore\n", out)
	assert.Contains(t, errOut, "skipped the Go refactor skill: "+dir+" has no go.mod")
	assert.NoDirExists(t, filepath.Join(dir, ".claude", "skills"))
}

//nolint:paralleltest // t.Chdir changes the directory of the whole process
func TestInit_FromASubdirectoryUsesTheRepositoryRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH, so init from a subdirectory of a repository did not run")
	}

	root := goProject(t)

	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir = root
	require.NoError(t, cmd.Run())

	sub := filepath.Join(root, "calc")
	require.NoError(t, os.MkdirAll(sub, 0o750))
	t.Chdir(sub)

	code, _, errOut := invoke(t, "", "init", "--bin", "/opt/wavez/wavez")
	require.Equal(t, 0, code, errOut)

	assert.FileExists(t, filepath.Join(root, ".claude", "settings.json"))
	assert.FileExists(t, filepath.Join(root, ".wavez", ".gitignore"))
	assert.NoDirExists(t, filepath.Join(sub, ".claude"), "Claude Code never reads settings from a subdirectory")
	assert.NoDirExists(t, filepath.Join(sub, ".wavez"))

	// --dir is the person's own choice and is taken as given.
	code, _, errOut = invoke(t, "", "init", "--dir", ".", "--bin", "/opt/wavez/wavez")
	require.Equal(t, 0, code, errOut)
	assert.FileExists(t, filepath.Join(sub, ".claude", "settings.json"))
}

func TestInit_WarnsWhenTheHooksWouldNotFindWavez(t *testing.T) {
	dir := goProject(t)
	t.Setenv("PATH", t.TempDir())

	code, _, errOut := invoke(t, "", "init", "--dir", dir)
	require.Equal(t, 0, code)
	assert.Contains(t, errOut, "warning: wavez is not on PATH")
	assert.Contains(t, errOut, "wavez init --bin ")

	code, _, errOut = invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez")
	require.Equal(t, 0, code)
	assert.NotContains(t, errOut, "warning", "a pinned binary needs no PATH")
}

func TestInit_Failures(t *testing.T) {
	t.Parallel()

	t.Run("an unknown flag is a usage error", func(t *testing.T) {
		t.Parallel()

		code, _, errOut := invoke(t, "", "init", "--nope")
		assert.Equal(t, 2, code)
		assert.Contains(t, errOut, "-nope")
	})

	t.Run("--help is not an error", func(t *testing.T) {
		t.Parallel()

		code, _, errOut := invoke(t, "", "init", "--help")
		assert.Equal(t, 0, code)
		assert.Contains(t, errOut, "-bin")
		assert.Contains(t, errOut, "-remove")
	})

	t.Run("a positional argument is rejected, not ignored", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		code, out, errOut := invoke(t, "", "init", "--dir", dir, "extra")
		assert.Equal(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errOut, `unexpected argument "extra"`)
		assert.NoDirExists(t, filepath.Join(dir, ".claude"))
	})

	t.Run("a --dir that does not exist is refused, not created", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "nope")

		code, out, errOut := invoke(t, "", "init", "--dir", dir)
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Contains(t, errOut, "is not an existing directory")
		assert.NoDirExists(t, dir)
	})

	t.Run("settings that cannot be parsed are left alone", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		settings := filepath.Join(dir, ".claude", "settings.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o750))
		require.NoError(t, os.WriteFile(settings, []byte("{oops"), 0o600))

		code, out, errOut := invoke(t, "", "init", "--dir", dir)
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Contains(t, errOut, "settings.json")
		assert.Equal(t, "{oops", readFile(t, settings))
	})
}

func TestInit_RemoveTakesOutWhatInitPutIn(t *testing.T) {
	t.Parallel()

	const theirs = `{
    "permissions": {"allow": ["Bash(go test:*)"], "deny": []},
    "hooks": {
        "Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}],
        "PostToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "prettier --write"}]}]
    },
    "model": "sonnet"
}
`

	tests := []struct {
		name   string
		before string
		// left is what the removal reports; settings is whether the file
		// is still there after it.
		left     string
		settings bool
	}{
		{
			name: "a project with hooks of its own keeps them and nothing else changes", before: theirs,
			left:     "updated .claude/settings.json\nremoved .claude/skills/wavez-refactor/SKILL.md\n",
			settings: true,
		},
		{
			name: "a project with settings and no hooks gets its settings back", before: `{"model": "sonnet"}` + "\n",
			left:     "updated .claude/settings.json\nremoved .claude/skills/wavez-refactor/SKILL.md\n",
			settings: true,
		},
		{
			name: "a project with no settings is left with none",
			left: "removed .claude/settings.json\nremoved .claude/skills/wavez-refactor/SKILL.md\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := goProject(t)
			settings := filepath.Join(dir, ".claude", "settings.json")

			if tt.before != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o750))
				require.NoError(t, os.WriteFile(settings, []byte(tt.before), 0o600))
			}

			code, _, errOut := invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez")
			require.Equal(t, 0, code, errOut)
			require.Contains(t, readFile(t, settings), "/opt/wavez/wavez hook")

			code, out, errOut := invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez", "--remove")
			require.Equal(t, 0, code, errOut)
			assert.Equal(t, tt.left, out)
			assert.Contains(t, errOut, "left .wavez/ alone")

			assert.NoDirExists(t, filepath.Join(dir, ".claude", "skills"))
			assert.FileExists(t, filepath.Join(dir, ".wavez", ".gitignore"))

			if !tt.settings {
				assert.NoDirExists(t, filepath.Join(dir, ".claude"), "the directory init made goes with its last file")

				return
			}

			after := readFile(t, settings)
			assert.NotContains(t, after, "wavez")

			var want, got any

			require.NoError(t, json.Unmarshal([]byte(tt.before), &want))
			require.NoError(t, json.Unmarshal([]byte(after), &got))
			assert.Equal(t, want, got, "init and then --remove is the file the project started with")

			if !strings.Contains(tt.before, `"hooks"`) {
				assert.Equal(t, tt.before, after, "a file that had no hooks comes back byte for byte")
			}

			code, out, _ = invoke(t, "", "init", "--dir", dir, "--bin", "/opt/wavez/wavez", "--remove")
			require.Equal(t, 0, code)
			assert.Equal(t, "nothing to remove: no wavez hook or skill is installed here\n", out)
		})
	}
}

func TestInit_StopHookTimeoutFollowsTheConfiguredBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want string
	}{
		{"no file", "", `"timeout": 180`},
		{"a configured budget", "stop_budget = \"1m\"\n", `"timeout": 90`},
		{"a file that does not load keeps the default", "stop_budget = \"soon\"\n", `"timeout": 180`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := goProject(t)
			if tt.file != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".wavez.toml"), []byte(tt.file), 0o600))
			}

			code, out, errOut := invoke(t, "", "init", "--dir", dir, "--print")
			require.Equal(t, 0, code, errOut)
			assert.Contains(t, out, tt.want)
			assert.Contains(t, out, `"statusMessage": "wavez: running checks"`)
		})
	}
}
