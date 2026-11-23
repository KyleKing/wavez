package goal_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/scripttest"
)

const (
	goMod    = "module example.com/scratch\n\ngo 1.24\n"
	pkgCode  = "package p\n\n// One returns one.\nfunc One() int { return 1 }\n"
	pkgTests = `package p

import "testing"

func TestOne(t *testing.T) {
	if One() != 1 {
		t.Fatal("bad")
	}
}

func TestBad(t *testing.T) { t.Fatalf("want %d", 2) }

func TestSkipped(t *testing.T) { t.Skip("not here") }
`
	brokenTests = "package q\n\nimport \"testing\"\n\nfunc TestQ(t *testing.T) { missing() }\n"
)

// evaluate loads root's goal and checks all of it.
func evaluate(t *testing.T, root string, expensive bool) goal.Report {
	t.Helper()

	loaded, err := goal.Load(root)
	require.NoError(t, err)

	report, err := goal.Evaluate(t.Context(), root, loaded, goal.Options{Expensive: expensive})
	require.NoError(t, err)

	return report
}

// byName indexes a report's results.
func byName(report goal.Report) map[string]goal.Result {
	out := map[string]goal.Result{}
	for _, result := range report.Results {
		out[result.Name] = result
	}

	return out
}

type want struct {
	name   string
	status goal.Status
	detail string
}

func assertResults(t *testing.T, report goal.Report, wants []want) {
	t.Helper()

	got := byName(report)
	require.Len(t, got, len(wants))

	for _, w := range wants {
		assert.Equal(t, w.status, got[w.name].Status, "%s: %s", w.name, got[w.name].Detail)
		assert.Contains(t, got[w.name].Detail, w.detail, w.name)

		if w.detail == "" {
			assert.Empty(t, got[w.name].Detail, w.name)
		}
	}
}

func item(name, check string) string {
	return "\n[[done]]\nname = \"" + name + "\"\n" + check + "\n"
}

func TestEvaluate_CheapKinds(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"README.md":       "ParseGlob refuses an empty pattern.\n",
		"src/a.go":        "package a\n",
		"src/deep/b.go":   "package b\n\n// TODO(glob): later\n",
		".wavez/skip.go":  "// TODO(glob): wavez's own state is not the project\n",
		"docs/is-dir/x":   "",
		"other/c.go":      "// TODO(glob)\n",
		"src/not-go.text": "TODO(glob)\n",
		goalFile: header +
			item("file there", `file = "README.md"`) +
			item("dir there", `file = "docs/is-dir"`) +
			item("file missing", `file = "docs/glob.md"`) +
			item("grep found", `grep = { file = "README.md", pattern = "empty\\s+pattern" }`) +
			item("grep not found", `grep = { file = "README.md", pattern = "glob\\.md" }`) +
			item("grep no file", `grep = { file = "NOTES.md", pattern = "x" }`) +
			item("grep a directory", `grep = { file = "docs", pattern = "x" }`) +
			item("absent holds", `absent = { glob = "src/*.go", pattern = 'TODO\(glob\)' }`) +
			item("absent broken", `absent = { glob = "src/**/*.go", pattern = 'TODO\(glob\)' }`) +
			item("absent nowhere to look", `absent = { glob = "nope/**/*.go", pattern = "x" }`) +
			item("absent any depth", `absent = { glob = "*.text", pattern = "TODO" }`),
	})

	wants := []want{
		{"file there", goal.StatusDone, ""},
		{"dir there", goal.StatusDone, ""},
		{"file missing", goal.StatusNotDone, "docs/glob.md does not exist"},
		{"grep found", goal.StatusDone, ""},
		{"grep not found", goal.StatusNotDone, `no match for /glob\.md/ in README.md`},
		{"grep no file", goal.StatusNotDone, "NOTES.md does not exist"},
		{"grep a directory", goal.StatusCouldNotCheck, "docs"},
		{"absent holds", goal.StatusDone, ""},
		{"absent broken", goal.StatusNotDone, "src/deep/b.go:3 matches"},
		{"absent nowhere to look", goal.StatusDone, ""},
		{"absent any depth", goal.StatusNotDone, "src/not-go.text:1 matches"},
	}

	// The cheap kinds answer the same whether or not the expensive ones run.
	assertResults(t, evaluate(t, root, false), wants)
	assertResults(t, evaluate(t, root, true), wants)
	assert.NoFileExists(t, goal.StatePath(root), "a goal with nothing expensive caches nothing")
}

func TestEvaluate_SymlinkOutOfTheRootIsNotFollowed(t *testing.T) {
	t.Parallel()

	outside := project(t, map[string]string{"secret.txt": "password\n"})
	root := project(t, map[string]string{
		goalFile: header + item("leak", `grep = { file = "link.txt", pattern = "password" }`),
	})
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")))

	assertResults(t, evaluate(t, root, true), []want{{"leak", goal.StatusCouldNotCheck, "link.txt"}})
}

func TestEvaluate_Test(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"go.mod": goMod, "p/p.go": pkgCode, "p/p_test.go": pkgTests, "q/q_test.go": brokenTests,
		goalFile: header +
			item("passes", `test = "./p -run TestOne"`) +
			item("fails", `test = "./p -run TestBad"`) +
			item("matches nothing", `test = "./p -run TestNoSuchTest"`) +
			item("only skipped", `test = "./p -run TestSkipped"`) +
			item("does not build", `test = "./q"`) +
			item("no such package", `test = "./nope -run TestOne"`) +
			item("times out", "test = \"./p -run TestOne\"\ntimeout = \"1ms\""),
	})

	assertResults(t, evaluate(t, root, true), []want{
		{"passes", goal.StatusDone, ""},
		{"fails", goal.StatusNotDone, "TestBad failed: p_test.go:11: want 2"},
		{"matches nothing", goal.StatusNotDone, "no test matched -run TestNoSuchTest"},
		{"only skipped", goal.StatusNotDone, "no test passed: 1 skipped"},
		{"does not build", goal.StatusNotDone, "does not build: q/q_test.go:5:28: undefined: missing"},
		{"no such package", goal.StatusCouldNotCheck, "nope"},
		{"times out", goal.StatusCouldNotCheck, "timed out after 1ms"},
	})
}

func TestEvaluate_TestOutsideAModuleAbstains(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{"p/p.go": pkgCode, goalFile: header + item("no module", `test = "./p"`)})

	assertResults(t, evaluate(t, root, true), []want{{"no module", goal.StatusCouldNotCheck, "go"}})
}

func TestEvaluate_Command(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		goalFile: header +
			item("exits 0", `command = "./check.sh pass"`) +
			item("exits 1", `command = "./check.sh fail"`) +
			item("silent failure", `command = "exit 3"`) +
			item("missing binary", `command = "wavez-no-such-linter run ./..."`) +
			item("times out", "command = \"sleep 30 & sleep 30\"\ntimeout = \"100ms\""),
	})
	scripttest.Write(t, filepath.Join(root, "check.sh"),
		"#!/bin/sh\nif [ \"$1\" = pass ]; then exit 0; fi\n"+
			"printf '\\n'\necho 'main.go:3: unused variable'\necho more\nexit 1\n")

	start := time.Now()

	assertResults(t, evaluate(t, root, true), []want{
		{"exits 0", goal.StatusDone, ""},
		{"exits 1", goal.StatusNotDone, "main.go:3: unused variable"},
		{"silent failure", goal.StatusNotDone, "exit status 3"},
		{"missing binary", goal.StatusCouldNotCheck, "wavez-no-such-linter"},
		{"times out", goal.StatusCouldNotCheck, "timed out after 100ms"},
	})
	assert.Less(t, time.Since(start), 20*time.Second, "a timeout kills the whole process group, not only the shell")
}

func TestEvaluate_CacheIsStaleAfterAFileChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		git  bool
	}{
		{name: "in a git repository", git: true},
		{name: "outside one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, map[string]string{
				"flag.txt": "ok\n",
				goalFile: header +
					item("flag is ok", `command = "grep -q ok flag.txt"`) + item("there", `file = "flag.txt"`),
			})

			if tt.git {
				initRepo(t, root)
			}

			unknown := []want{{"flag is ok", goal.StatusUnknown, "runs at end of turn"}, {"there", goal.StatusDone, ""}}
			done := []want{{"flag is ok", goal.StatusDone, ""}, {"there", goal.StatusDone, ""}}

			assertResults(t, evaluate(t, root, false), unknown)
			assertResults(t, evaluate(t, root, true), done)
			assertResults(t, evaluate(t, root, false), done)

			// The file is already listed as changed, so only its size and
			// time tell this edit from the last state.
			require.NoError(t, os.WriteFile(filepath.Join(root, "flag.txt"), []byte("changed\n"), 0o600))
			assertResults(t, evaluate(t, root, false), unknown)

			assertResults(t, evaluate(t, root, true), []want{
				{"flag is ok", goal.StatusNotDone, "exit status 1"}, {"there", goal.StatusDone, ""},
			})
			assertResults(t, evaluate(t, root, false), []want{
				{"flag is ok", goal.StatusNotDone, "exit status 1"}, {"there", goal.StatusDone, ""},
			})
		})
	}
}

func TestEvaluate_CacheIsDroppedWhenTheConditionIsEdited(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{goalFile: header + item("passes", `command = "true"`)})

	assertResults(t, evaluate(t, root, true), []want{{"passes", goal.StatusDone, ""}})

	rewritten := header + item("passes", `command = "false"`)
	require.NoError(t, os.WriteFile(filepath.Join(root, goalFile), []byte(rewritten), 0o600))

	assertResults(t, evaluate(t, root, false), []want{{"passes", goal.StatusUnknown, "runs at end of turn"}})
}

func TestEvaluate_CanceledRunCachesNothing(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{goalFile: header + item("passes", `command = "true"`)})

	loaded, err := goal.Load(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	report, err := goal.Evaluate(ctx, root, loaded, goal.Options{Expensive: true})
	require.NoError(t, err)
	assertResults(t, report, []want{{"passes", goal.StatusCouldNotCheck, "canceled"}})
	assert.NoFileExists(t, goal.StatePath(root))
}

// initRepo makes root a git repository with nothing committed.
func initRepo(t *testing.T, root string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "init", "--quiet")
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
