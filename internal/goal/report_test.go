package goal_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/scripttest"
)

func TestRender(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"README.md": "hello\n",
		goalFile: header +
			item("readme", `file = "README.md"`) +
			item("documented", `grep = { file = "README.md", pattern = "empty pattern" }`) +
			item("lints clean", `command = "wavez-no-such-linter run"`) +
			item("builds", `command = "true"`),
	})

	cheap := evaluate(t, root, false)
	assert.Equal(t, "ParseGlob rejects an empty pattern\n"+
		"[x] readme\n"+
		"[ ] documented — no match for /empty pattern/ in README.md\n"+
		"[~] lints clean — not run yet: runs at end of turn\n"+
		"[~] builds — not run yet: runs at end of turn\n"+
		"1 of 4 done, 2 not run yet\n", cheap.Render())

	full := evaluate(t, root, true)
	lines := strings.Split(full.Render(), "\n")
	require.Len(t, lines, 7)
	assert.Contains(t, lines[3], "[?] lints clean — could not check: ")
	assert.Equal(t, "[x] builds", lines[4])
	assert.Equal(t, "2 of 4 done, 1 could not be checked", lines[5])

	assert.Equal(t, goal.Summary{
		Goal: "ParseGlob rejects an empty pattern", Unmet: []string{"documented"},
		Total: 4, Done: 2, CouldNotCheck: 1,
	}, full.Summary())
}

func TestSnapshot(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"flag.txt": "ok\n",
		goalFile: header +
			item("there", `file = "flag.txt"`) +
			item("documented", `file = "docs/glob.md"`) +
			item("flag is ok", `command = "grep -q ok flag.txt"`),
	})

	_, err := goal.Snapshot(t.TempDir())
	require.ErrorIs(t, err, goal.ErrNoGoal)

	got, err := goal.Snapshot(root)
	require.NoError(t, err)
	assert.Equal(t, goal.Summary{
		Goal: "ParseGlob rejects an empty pattern", Unmet: []string{"documented"},
		Total: 3, Done: 1, Unknown: 1, Stale: true,
	}, got, "nothing expensive has run")

	evaluate(t, root, true)

	got, err = goal.Snapshot(root)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Done)
	assert.False(t, got.Stale)

	// A cheap condition is read now, not from the cache.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "glob.md"), []byte("x\n"), 0o600))

	got, err = goal.Snapshot(root)
	require.NoError(t, err)
	assert.Equal(t, 3, got.Done)
	assert.Empty(t, got.Unmet)

	// An edited condition has no verdict until it runs again.
	rewritten := header + item("flag is ok", `command = "grep -q broken flag.txt"`)
	require.NoError(t, os.WriteFile(filepath.Join(root, goalFile), []byte(rewritten), 0o600))

	got, err = goal.Snapshot(root)
	require.NoError(t, err)
	assert.Equal(t, goal.Summary{
		Goal: "ParseGlob rejects an empty pattern", Unmet: []string{}, Total: 1, Unknown: 1, Stale: true,
	}, got)
}

// A status line calls Snapshot on every refresh, so it may not start a
// process: every program it could reach here leaves a mark when run.
func TestSnapshot_StartsNoProcess(t *testing.T) {
	root := project(t, map[string]string{
		"go.mod": goMod, "p/p.go": pkgCode, "p/p_test.go": pkgTests,
		goalFile: header +
			item("passes", `test = "./p -run TestOne"`) +
			item("exits 0", `command = "true"`) +
			item("there", `file = "go.mod"`) +
			item("named", `grep = { file = "go.mod", pattern = "scratch" }`) +
			item("no TODO", `absent = { glob = "**/*.go", pattern = "TODO" }`),
	})

	evaluate(t, root, true)

	bin := t.TempDir()
	mark := filepath.Join(t.TempDir(), "ran")

	for _, name := range []string{"git", "go", "sh"} {
		scripttest.Write(t, filepath.Join(bin, name), "#!/bin/sh\necho "+name+" >> '"+mark+"'\nexit 1\n")
	}

	t.Setenv("PATH", bin)

	got, err := goal.Snapshot(root)
	require.NoError(t, err)
	assert.Equal(t, 5, got.Done, "the expensive verdicts come from the cache")
	assert.False(t, got.Stale)
	assert.NoFileExists(t, mark)
}

func TestStopReport(t *testing.T) {
	t.Parallel()

	const (
		done    = "\n[[done]]\nname = \"readme\"\nfile = \"README.md\"\n"
		unmet   = "\n[[done]]\nname = \"documented\"\ngrep = { file = \"README.md\", pattern = \"empty pattern\" }\n"
		failing = "\n[[done]]\nname = \"flag is ok\"\ncommand = \"echo 'flag.txt: not ok'; exit 1\"\n"
		unknown = "\n[[done]]\nname = \"lints clean\"\ncommand = \"wavez-no-such-linter run\"\n"
	)

	tests := []struct {
		name   string
		file   string
		person string
		agent  string
		block  bool
	}{
		{name: "no goal says nothing"},
		{name: "all done", file: header + "block = true\n" + done, person: "goal 1/1 done"},
		{name: "no conditions", file: header + "block = true\n", person: "goal 0/0: no conditions yet"},
		{
			name: "unmet without block tells only the person", file: header + done + unmet + failing,
			person: "goal 1/3: not done: documented, flag is ok",
		},
		{
			name: "unmet with block holds the turn", file: header + "block = true\n" + done + unmet + failing + unknown,
			person: "goal 1/4: not done: documented, flag is ok; could not check: lints clean",
			agent: "Goal not done: ParseGlob rejects an empty pattern\n" +
				"- documented: no match for /empty pattern/ in README.md\n" +
				"- flag is ok: flag.txt: not ok\n",
			block: true,
		},
		{
			name: "what could not be checked never blocks", file: header + "block = true\n" + done + unknown,
			person: "goal 1/2: could not check: lints clean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{"README.md": "hello\n"}
			if tt.file != "" {
				files[goalFile] = tt.file
			}

			person, agent, block, err := goal.StopReport(t.Context(), project(t, files))
			require.NoError(t, err)
			assert.Equal(t, tt.person, person)
			assert.Equal(t, tt.agent, agent)
			assert.Equal(t, tt.block, block)
		})
	}
}

func TestStopReport_InvalidGoalIsAnErrorAndNeverABlock(t *testing.T) {
	t.Parallel()

	person, agent, block, err := goal.StopReport(t.Context(), project(t, map[string]string{goalFile: "block = true\n"}))

	var invalid *goal.Error

	require.ErrorAs(t, err, &invalid)
	assert.Empty(t, person)
	assert.Empty(t, agent)
	assert.False(t, block)
}

func TestStopReport_AgentTextIsCapped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		more  string
		items int
	}{
		{name: "a few long conditions fit", items: 2},
		{name: "many are counted, not listed", items: 60, more: "- and 5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var file strings.Builder

			file.WriteString("goal = \"" + strings.Repeat("a very long sentence ", 40) + "\"\nblock = true\n")

			for index := range tt.items {
				file.WriteString(item(fmt.Sprintf("condition %d %s", index, strings.Repeat("ü", 150)),
					fmt.Sprintf(`file = "missing/%d/%s"`, index, strings.Repeat("x", 100))))
			}

			root := project(t, map[string]string{goalFile: file.String()})

			person, agent, block, err := goal.StopReport(t.Context(), root)
			require.NoError(t, err)
			assert.True(t, block)
			assert.LessOrEqual(t, len(agent), goal.AgentLimit)
			assert.LessOrEqual(t, len(person), 200)
			assert.True(t, strings.HasPrefix(agent, "Goal not done: a very long sentence "), agent)
			assert.Contains(t, agent, "- condition 0 ")
			assert.Contains(t, agent, tt.more)
			assert.True(t, strings.HasSuffix(agent, "\n"))
			assert.True(t, utf8.ValidString(agent) && utf8.ValidString(person), "a cut never splits a rune")
		})
	}
}
