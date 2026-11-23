package hook_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/hook"
)

const skill = "---\nname: wavez-refactor\ndescription: test\n---\nbody\n"

// foreign is a settings file a person wrote: keys wavez knows nothing
// about, in an order of their own, and hooks from other tools on the same
// events wavez uses.
const foreign = `{
  "zeta": {"b": 1, "a": [1.50, 12345678901234567890, null, true]},
  "permissions": {"allow": ["Bash(go test:*)", "Read(~/.zshrc)"], "deny": []},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh && echo <ok>"}]}],
    "PostToolUse": [
      {"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "prettier --write", "timeout": 30}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}]
  },
  "alpha": "first shall be last"
}
`

func read(t *testing.T, dir, rel string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // a file the test wrote
	require.NoError(t, err)

	return string(raw)
}

// install plans and applies, returning "action path" per change.
func install(t *testing.T, in hook.Install) []string {
	t.Helper()

	changes, err := in.Plan()
	require.NoError(t, err)
	require.NoError(t, in.Apply(changes))

	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, change.Action()+" "+change.Path)
	}

	return lines
}

// commands lists the hook commands settings.json holds for an event.
func commands(t *testing.T, dir, event string) []string {
	t.Helper()

	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}

	require.NoError(t, json.Unmarshal([]byte(read(t, dir, hook.SettingsPath)), &settings))

	var out []string

	for _, group := range settings.Hooks[event] {
		for _, handler := range group.Hooks {
			assert.Equal(t, "command", handler.Type)

			out = append(out, strings.TrimSpace(group.Matcher+" => "+handler.Command))
		}
	}

	return out
}

func TestInstall_FreshProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := hook.Install{Dir: dir, Skill: skill}

	assert.Equal(t, []string{
		"created .claude/settings.json",
		"created .claude/skills/wavez-refactor/SKILL.md",
		"created .wavez/.gitignore",
	}, install(t, in))

	assert.JSONEq(t, `{"hooks": {
		"PostToolUse": [{
			"matcher": "Edit|Write|MultiEdit|NotebookEdit",
			"hooks": [{"type": "command", "command": "wavez hook", "timeout": 40}]
		}],
		"Stop": [{"hooks": [{
			"type": "command", "command": "wavez hook", "timeout": 180, "statusMessage": "wavez: running checks"
		}]}],
		"SessionStart": [{"hooks": [{"type": "command", "command": "wavez hook", "timeout": 30}]}],
		"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "wavez hook", "timeout": 10}]}],
		"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "wavez hook", "timeout": 10}]}]
	}}`, read(t, dir, hook.SettingsPath))
	assert.Equal(t, skill, read(t, dir, hook.SkillPath))
	assert.Equal(t, "*\n", read(t, dir, hook.GitignorePath))

	assert.Empty(t, install(t, in), "a second run has nothing to do")
}

func TestInstall_PlanWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	changes, err := hook.Install{Dir: dir, Skill: skill}.Plan()
	require.NoError(t, err)
	require.Len(t, changes, 3)
	assert.True(t, changes[0].Created)
	assert.Contains(t, string(changes[0].Content), `"command": "wavez hook"`)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestInstall_KeepsForeignSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, hook.SettingsPath, foreign)
	require.NoError(t, os.Chmod(filepath.Join(dir, hook.SettingsPath), 0o400))

	in := hook.Install{Dir: dir, Skill: skill}
	assert.Equal(t, "updated .claude/settings.json", install(t, in)[0])

	got := read(t, dir, hook.SettingsPath)

	var want, have map[string]any

	require.NoError(t, json.Unmarshal([]byte(foreign), &want))
	require.NoError(t, json.Unmarshal([]byte(got), &have))

	for _, key := range []string{"zeta", "permissions", "alpha"} {
		assert.Equal(t, want[key], have[key], key)
	}

	assert.Contains(t, got, "12345678901234567890", "a number too large for a float must not be rounded")
	assert.Contains(t, got, "1.50", "a number keeps the spelling it had")
	assert.Contains(t, got, "guard.sh && echo <ok>", "shell operators are not HTML-escaped")
	assert.Less(t, strings.Index(got, `"zeta"`), strings.Index(got, `"permissions"`), "key order is the author's")
	assert.Less(t, strings.Index(got, `"hooks"`), strings.Index(got, `"alpha"`))

	assert.Equal(t, []string{"Bash => guard.sh && echo <ok>", "Bash => wavez hook"}, commands(t, dir, "PreToolUse"),
		"a foreign Bash guard keeps its group and wavez gets one of its own")
	assert.Equal(t, []string{"=> wavez hook"}, commands(t, dir, "UserPromptSubmit"))
	assert.Equal(t, []string{
		"Edit|Write => prettier --write",
		"Edit|Write|MultiEdit|NotebookEdit => wavez hook",
	}, commands(t, dir, "PostToolUse"))
	assert.Equal(t, []string{"=> notify-send done", "=> wavez hook"}, commands(t, dir, "Stop"))
	assert.Equal(t, []string{"=> wavez hook"}, commands(t, dir, "SessionStart"))
	assert.Contains(t, got, `"timeout": 30`)

	info, err := os.Stat(filepath.Join(dir, hook.SettingsPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o400), info.Mode().Perm(), "an existing file keeps its mode")

	assert.Empty(t, install(t, in), "a second run has nothing to do")
	assert.Equal(t, got, read(t, dir, hook.SettingsPath))
}

func TestInstall_BinChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, hook.SettingsPath, foreign)

	install(t, hook.Install{Dir: dir, Skill: skill})

	pinned := filepath.Join(dir, "bin dir", "wavez-bench")
	command := "'" + pinned + "' hook"

	rewritten := []string{"updated .claude/settings.json"}
	assert.Equal(t, rewritten, install(t, hook.Install{Dir: dir, Skill: skill, Bin: pinned}))
	assert.Equal(t, []string{
		"Edit|Write => prettier --write",
		"Edit|Write|MultiEdit|NotebookEdit => " + command,
	}, commands(t, dir, "PostToolUse"))
	assert.Equal(t, []string{"=> notify-send done", "=> " + command}, commands(t, dir, "Stop"))
	assert.Equal(t, []string{"=> " + command}, commands(t, dir, "SessionStart"))

	assert.Empty(t, install(t, hook.Install{Dir: dir, Skill: skill, Bin: pinned}))

	assert.Equal(t, rewritten, install(t, hook.Install{Dir: dir, Skill: skill}))
	assert.Equal(t, []string{"=> notify-send done", "=> wavez hook"}, commands(t, dir, "Stop"))
}

func TestInstall_Command(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs("build/wavez")
	require.NoError(t, err)

	tests := []struct {
		name string
		bin  string
		want string
	}{
		{name: "default is found on PATH", bin: "", want: "wavez hook"},
		{name: "a bare name stays a PATH lookup", bin: "wavez-dev", want: "wavez-dev hook"},
		{name: "a relative path is made absolute", bin: "build/wavez", want: abs + " hook"},
		{name: "an absolute path is kept", bin: "/opt/wavez/bin/wavez", want: "/opt/wavez/bin/wavez hook"},
		{name: "a path with a space is one shell word", bin: "/opt/my tools/wavez", want: "'/opt/my tools/wavez' hook"},
		{name: "a quote in the path is escaped", bin: "/opt/it's/wavez", want: `'/opt/it'\''s/wavez' hook`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := hook.Install{Bin: tt.bin}.Command()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInstall_RepairsItsOwnEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
		event    string
		want     []string
	}{
		{
			name: "duplicates collapse to one",
			settings: `{"hooks": {"Stop": [
				{"hooks": [{"type": "command", "command": "wavez hook"}]},
				{"hooks": [{"type": "command", "command": "other"}]},
				{"hooks": [
					{"type": "command", "command": "/old/path/wavez hook"},
					{"type": "command", "command": "wavez hook"}
				]}
			]}}`,
			event: "Stop",
			want:  []string{"=> wavez hook", "=> other"},
		},
		{
			name: "an entry sharing a group with a foreign hook moves to its own",
			settings: `{"hooks": {"PostToolUse": [
				{"matcher": "Write", "hooks": [
					{"type": "command", "command": "fmt.sh"},
					{"type": "command", "command": "'/x y/wavez' hook"}
				]}
			]}}`,
			event: "PostToolUse",
			want:  []string{"Write => fmt.sh", "Edit|Write|MultiEdit|NotebookEdit => wavez hook"},
		},
		{
			name: "a stale matcher is corrected and a hand-set timeout kept",
			settings: `{"hooks": {"PostToolUse": [
				{"matcher": "Edit", "hooks": [{"type": "command", "command": "wavez hook", "timeout": 5}]}
			]}}`,
			event: "PostToolUse",
			want:  []string{"Edit|Write|MultiEdit|NotebookEdit => wavez hook"},
		},
		{
			name: "a matcher on an event that takes none is dropped",
			settings: `{"hooks": {"Stop": [
				{"matcher": "x", "hooks": [{"type": "command", "command": "wavez hook"}]}
			]}}`,
			event: "Stop",
			want:  []string{"=> wavez hook"},
		},
		{
			name: "commands that only look similar are left alone",
			settings: `{"hooks": {"Stop": [
				{"hooks": [
					{"type": "command", "command": "wavez hook --verbose"},
					{"type": "command", "command": "git hook"},
					{"type": "prompt", "command": "wavez hook"},
					"not an object"
				]}
			]}}`,
			event: "Stop",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			write(t, dir, hook.SettingsPath, tt.settings)

			in := hook.Install{Dir: dir, Skill: skill}
			install(t, in)

			if tt.want != nil {
				assert.Equal(t, tt.want, commands(t, dir, tt.event))
			} else {
				got := read(t, dir, hook.SettingsPath)
				for _, kept := range []string{"wavez hook --verbose", "git hook", `"prompt"`, `"not an object"`} {
					assert.Contains(t, got, kept)
				}

				assert.Equal(t, 6, strings.Count(got, `"wavez hook"`), "the prompt hook's, plus one per event")
			}

			assert.Empty(t, install(t, in), "a second run has nothing to do")
		})
	}

	t.Run("the kept timeout survives", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, dir, hook.SettingsPath, tests[2].settings)
		install(t, hook.Install{Dir: dir, Skill: skill})
		assert.Contains(t, read(t, dir, hook.SettingsPath), `"timeout": 5`)
	})
}

func TestInstall_AlreadyInstalledFileKeepsItsFormatting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := hook.Install{Dir: dir, Skill: skill}
	install(t, in)

	var compact any

	require.NoError(t, json.Unmarshal([]byte(read(t, dir, hook.SettingsPath)), &compact))

	oneLine, err := json.Marshal(compact)
	require.NoError(t, err)

	// encoding/json sorts keys, so this is the same settings in another
	// order and layout: nothing wavez needs to touch.
	write(t, dir, hook.SettingsPath, string(oneLine))

	assert.Empty(t, install(t, in))
	assert.Equal(t, string(oneLine), read(t, dir, hook.SettingsPath))
}

func TestInstall_EmptySettingsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, hook.SettingsPath, "  \n")

	assert.Equal(t, "updated .claude/settings.json", install(t, hook.Install{Dir: dir, Skill: skill})[0])
	assert.Equal(t, []string{"=> wavez hook"}, commands(t, dir, "Stop"))
}

func TestInstall_RefusesSettingsItCannotRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
	}{
		{name: "not JSON", settings: `{"hooks": `},
		{name: "a comment", settings: "// mine\n{}"},
		{name: "two values", settings: `{} {}`},
		{name: "top level is an array", settings: `[]`},
		{name: "hooks is not an object", settings: `{"hooks": []}`},
		{name: "an event is not an array", settings: `{"hooks": {"Stop": {}}}`},
		{name: "a group is not an object", settings: `{"hooks": {"Stop": ["x"]}}`},
		{name: "unbalanced array", settings: `{"hooks": {"Stop": [}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			write(t, dir, hook.SettingsPath, tt.settings)

			changes, err := hook.Install{Dir: dir, Skill: skill}.Plan()
			require.ErrorIs(t, err, hook.ErrSettingsShape)
			assert.Empty(t, changes)
			assert.Equal(t, tt.settings, read(t, dir, hook.SettingsPath))
			assert.NoFileExists(t, filepath.Join(dir, hook.SkillPath), "nothing is installed beside it")
		})
	}
}

func TestInstall_UnwritableProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, ".claude", "a file where the directory should be")

	in := hook.Install{Dir: dir, Skill: skill}

	_, err := in.Plan()
	require.Error(t, err)

	err = in.Apply([]hook.Change{{Path: hook.SettingsPath, Content: []byte("{}")}})
	require.Error(t, err)
}

// timeouts maps each event to the timeout of its wavez handler.
func timeouts(t *testing.T, dir string) map[string]any {
	t.Helper()

	var settings struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}

	require.NoError(t, json.Unmarshal([]byte(read(t, dir, hook.SettingsPath)), &settings))

	out := map[string]any{}

	for event, groups := range settings.Hooks {
		for _, group := range groups {
			for _, handler := range group.Hooks {
				if command, ok := handler["command"].(string); ok && strings.HasSuffix(command, " hook") {
					out[event] = handler["timeout"]
					out[event+" status"] = handler["statusMessage"]
				}
			}
		}
	}

	return out
}

func TestInstall_EveryHookGetsATimeout_AndAPersonsOwnIsKept(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want   map[string]any
		name   string
		before string
	}{
		{
			name: "a fresh install: no hook is left on the harness's ten minutes",
			want: map[string]any{
				"PreToolUse": 10.0, "UserPromptSubmit": 10.0, "PostToolUse": 40.0, "SessionStart": 30.0,
				"Stop": 180.0, "Stop status": "wavez: running checks",
			},
		},
		{
			name:   "an install from before timeouts were written gets them",
			before: `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "wavez hook"}]}]}}`,
			want: map[string]any{
				"PreToolUse": 10.0, "UserPromptSubmit": 10.0, "PostToolUse": 40.0, "SessionStart": 30.0,
				"Stop": 180.0, "Stop status": "wavez: running checks",
			},
		},
		{
			name: "a timeout and a status message a person set are theirs",
			before: `{"hooks": {
				"Stop": [{"hooks": [{
					"type": "command", "command": "wavez hook", "timeout": 900, "statusMessage": "checking"
				}]}],
				"PreToolUse": [{
					"matcher": "Bash", "hooks": [{"type": "command", "command": "wavez hook", "timeout": 3}]
				}]
			}}`,
			want: map[string]any{
				"PreToolUse": 3.0, "UserPromptSubmit": 10.0, "PostToolUse": 40.0, "SessionStart": 30.0,
				"Stop": 900.0, "Stop status": "checking",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.before != "" {
				write(t, dir, hook.SettingsPath, tt.before)
			}

			in := hook.Install{Dir: dir, Skill: skill}
			install(t, in)

			got := timeouts(t, dir)
			for key, want := range tt.want {
				assert.Equal(t, want, got[key], key)
			}

			assert.Empty(t, install(t, in), "a second run has nothing to do")
		})
	}
}

func TestInstall_RewritingSettingsKeepsTheFilesOwnLayout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before string
		// lead is the indentation the new hooks must use.
		lead string
		// kept are lines of the file that must come back byte for byte.
		kept []string
	}{
		{
			name: "four spaces and one-line arrays, with no hooks yet",
			before: "{\n    \"permissions\": {\"allow\": [\"Bash(go test:*)\", \"Read(~/.zshrc)\"], \"deny\": []},\n" +
				"    \"model\": \"sonnet\"\n}\n",
			kept: []string{
				`    "permissions": {"allow": ["Bash(go test:*)", "Read(~/.zshrc)"], "deny": []},`,
				`    "model": "sonnet",`,
			},
			lead: "\n    \"hooks\": {\n        \"PostToolUse\": [",
		},
		{
			name: "tabs, with hooks of its own in the middle of the file",
			before: "{\n\t\"zeta\": {\"b\": 1, \"a\": [1.50, null]},\n" +
				"\t\"hooks\": {\"Stop\": [{\"hooks\": [" +
				"{\"type\": \"command\", \"command\": \"notify-send done\"}]}]},\n" +
				"\t\"alpha\": \"last\"\n}\n",
			kept: []string{"\t\"zeta\": {\"b\": 1, \"a\": [1.50, null]},", "\t\"alpha\": \"last\""},
			lead: "\n\t\"hooks\": {\n\t\t\"Stop\": [",
		},
		{
			name:   "an empty object",
			before: "{}\n",
			lead:   "{\n  \"hooks\": {\n    \"PostToolUse\": [",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			write(t, dir, hook.SettingsPath, tt.before)

			in := hook.Install{Dir: dir, Skill: skill}
			install(t, in)

			after := read(t, dir, hook.SettingsPath)

			for _, line := range tt.kept {
				assert.Contains(t, after, line+"\n", "a line outside hooks was laid out again")
			}

			assert.Contains(t, after, tt.lead)
			assert.True(t, json.Valid([]byte(after)), after)
			assert.Equal(t, 5, strings.Count(after, `"command": "wavez hook"`))
			assert.True(t, strings.HasSuffix(after, "}\n"))

			assert.Empty(t, install(t, in), "a second run has nothing to do")

			// Taking wavez back out leaves what the file said before.
			changes, err := in.PlanRemoval()
			require.NoError(t, err)
			require.NoError(t, in.Apply(changes))

			// A file left holding nothing is removed, which says the same
			// as the empty object it started as.
			//nolint:gosec // a file the test wrote
			left, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(hook.SettingsPath)))
			if errors.Is(err, fs.ErrNotExist) {
				left = []byte("{}")
			}

			var want, got any

			require.NoError(t, json.Unmarshal([]byte(tt.before), &want))
			require.NoError(t, json.Unmarshal(left, &got))
			assert.Equal(t, want, got)

			for _, line := range tt.kept {
				assert.Contains(t, string(left), strings.TrimSuffix(line, ",")+"",
					"removal laid out a line that was not wavez's")
			}
		})
	}
}

func TestInstall_RemovalLeavesWhatInitDidNotWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, hook.SettingsPath, foreign)

	in := hook.Install{Dir: dir, Skill: skill}
	install(t, in)

	// A person edits the skill after init wrote it: it is theirs now.
	write(t, dir, hook.SkillPath, skill+"\nMy own note.\n")

	changes, err := in.PlanRemoval()
	require.NoError(t, err)
	require.Len(t, changes, 1, "only the settings change; an edited skill is not init's to delete")
	require.NoError(t, in.Apply(changes))

	assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(hook.SkillPath)))
	assert.Equal(t, []string{"Bash => guard.sh && echo <ok>"}, commands(t, dir, "PreToolUse"))
	assert.Equal(t, []string{"Edit|Write => prettier --write"}, commands(t, dir, "PostToolUse"))
	assert.Equal(t, []string{"=> notify-send done"}, commands(t, dir, "Stop"))
	assert.Empty(t, commands(t, dir, "SessionStart"))

	var want, got any

	require.NoError(t, json.Unmarshal([]byte(foreign), &want))
	require.NoError(t, json.Unmarshal([]byte(read(t, dir, hook.SettingsPath)), &got))
	assert.Equal(t, want, got)

	again, err := in.PlanRemoval()
	require.NoError(t, err)
	assert.Empty(t, again)
}
