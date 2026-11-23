package gate_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/session"
)

const (
	srcOldTest = "package a\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Fatal(\"old trouble\") }\n"
	srcNewTest = "package a\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) { t.Fatal(\"new trouble\") }\n"
	srcExtra   = "package a\n\n// Extra is what the session added.\nfunc Extra() int { return 2 }\n"
	// A table whose first case always fails and whose second fails once A
	// is no longer one.
	srcTableTest = "package a\n\nimport \"testing\"\n\nfunc TestTable(t *testing.T) {\n" +
		"\tt.Run(\"always\", func(t *testing.T) { t.Fatal(\"old trouble\") })\n" +
		"\tt.Run(\"value\", func(t *testing.T) {\n\t\tif A() != 1 {\n\t\t\tt.Fatal(\"new trouble\")\n\t\t}\n\t})\n}\n"
)

// committed writes files into a fresh repository and commits them.
func committed(t *testing.T, files map[string]string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	files[".wavez/.gitignore"] = "*\n"
	root := project(t, files)
	ctx := t.Context()
	git(t, root, exec.CommandContext(ctx, "git", "init", "-q"))
	git(t, root, exec.CommandContext(ctx, "git", "add", "-A"))
	git(t, root, exec.CommandContext(ctx, "git", "commit", "-q", "-m", "base"))

	return root
}

// began returns a context carrying the tree at root as a session's
// baseline, taken the way a session takes it.
func began(t *testing.T, root string) context.Context {
	t.Helper()

	state, err := session.NewStore(root).Update(t.Context(), "gate-test", func(*session.State) error { return nil })
	require.NoError(t, err)
	require.True(t, state.Baseline.Restorable())

	return gate.WithBaseline(t.Context(), gate.Baseline{Commit: state.Baseline.Head, Tree: state.Baseline.Tree})
}

func result(t *testing.T, report gate.Report, check string) gate.Result {
	t.Helper()

	for i := range report.Results {
		if report.Results[i].Check == check {
			return report.Results[i]
		}
	}

	require.Failf(t, "no result", "%s is not in %+v", check, report.Results)

	return gate.Result{}
}

// comparison is one session over the fixture module: the tree it started
// from, what it then wrote, and what the stop gates must make of it.
type comparison struct {
	name string
	// base overrides the fixture module before the commit.
	base map[string]string
	// dirty is written after the commit and before the session starts.
	dirty map[string]string
	// during is what the session writes.
	during  map[string]string
	changed []string
	check   string
	status  gate.Status
	// existing is what the result names as failing before the session.
	existing []string
	// agent and person are substrings of what each is told; hidden must
	// not reach the agent.
	agent  []string
	hidden []string
	person []string
}

func runComparisons(t *testing.T, tests []comparison) {
	t.Helper()

	for i := range tests {
		tt := &tests[i]

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := committed(t, module(tt.base))
			apply(t, root, tt.dirty)

			ctx := began(t, root)
			apply(t, root, tt.during)

			before := status(t, root)

			report, err := gate.New().Run(ctx, root, tt.changed, gate.StageStop)
			require.NoError(t, err)

			got := result(t, report, tt.check)
			assert.Equal(t, tt.status, got.Status, "%+v", got)
			assert.Equal(t, tt.existing, got.Preexisting)
			assert.Equal(t, tt.status == gate.StatusFail, report.Failed())

			told(t, report.Render(), tt.agent, tt.hidden)
			told(t, report.Notes(), tt.person, nil)

			assert.Equal(t, before, status(t, root), "the working tree is left as the session had it")
			assert.Equal(t, 1, strings.Count(worktrees(t, root), "worktree "), "no worktree is left registered")
		})
	}
}

// apply writes files under root, removing the ones marked removed.
func apply(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		if body == removed {
			require.NoError(t, os.Remove(filepath.Join(root, filepath.FromSlash(name))))

			continue
		}

		writeFile(t, root, name, body)
	}
}

// told fails unless text holds every wanted substring and none of the
// hidden ones, and is empty when nothing is wanted.
func told(t *testing.T, text string, holds, hidden []string) {
	t.Helper()

	if len(holds) == 0 {
		assert.Empty(t, text)
	}

	for _, want := range holds {
		assert.Contains(t, text, want)
	}

	for _, not := range hidden {
		assert.NotContains(t, text, not)
	}
}

func status(t *testing.T, root string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	cmd.Dir = root

	out, err := cmd.Output()
	require.NoError(t, err)

	return string(out)
}

func worktrees(t *testing.T, root string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "worktree", "list", "--porcelain")
	cmd.Dir = root

	out, err := cmd.Output()
	require.NoError(t, err)

	return string(out)
}

func TestRun_TestsThatAlreadyFailedDoNotBlock(t *testing.T) {
	t.Parallel()

	const pkgA = "example.com/scratch/a"

	runComparisons(t, []comparison{
		{
			name:   "a test that was failing before the session is the person's to hear about",
			base:   map[string]string{"a/a_test.go": srcATestRed},
			during: map[string]string{"a/extra.go": srcExtra}, changed: []string{"a/extra.go"},
			check: "test", status: gate.StatusPreexisting, existing: []string{pkgA + " TestA"},
			person: []string{"ALREADY FAILING", "test: " + pkgA + " TestA"},
		},
		{
			name:   "a test the session broke still blocks",
			during: map[string]string{"a/a.go": srcAThree}, changed: []string{"a/a.go"},
			check: "test", status: gate.StatusFail,
			agent: []string{"FAIL test", "--- FAIL: TestA", "--- FAIL: TestB"},
		},
		{
			name:   "an old failure is taken out of what the agent is handed for a new one",
			base:   map[string]string{"a/old_test.go": srcOldTest},
			during: map[string]string{"a/a.go": srcAThree}, changed: []string{"a/a.go"},
			check: "test", status: gate.StatusFail, existing: []string{pkgA + " TestOld"},
			agent: []string{"--- FAIL: TestA", "--- FAIL: TestB"}, hidden: []string{"TestOld", "old trouble"},
			person: []string{pkgA + " TestOld"},
		},
		{
			name:   "a failing test the baseline does not have is new",
			during: map[string]string{"a/new_test.go": srcNewTest}, changed: []string{"a/new_test.go"},
			check: "test", status: gate.StatusFail, agent: []string{"--- FAIL: TestNew", "new trouble"},
		},
		{
			name:    "a test with one subtest the session broke blocks whole",
			base:    map[string]string{"a/table_test.go": srcTableTest},
			during:  map[string]string{"a/a.go": srcANine, "a/a_test.go": removed, "b/b_test.go": removed},
			changed: []string{"a/a.go"}, check: "test", status: gate.StatusFail,
			agent: []string{"TestTable/value", "TestTable/always", "--- FAIL: TestC"},
		},
		{
			name:   "a subtest that was failing before does not block by itself",
			base:   map[string]string{"a/table_test.go": srcTableTest},
			during: map[string]string{"a/extra.go": srcExtra}, changed: []string{"a/extra.go"},
			check: "test", status: gate.StatusPreexisting, existing: []string{pkgA + " TestTable"},
			person: []string{pkgA + " TestTable"},
		},
		{
			name:   "tests of a package that did not compile at the baseline could not be compared and block",
			base:   map[string]string{"a/a_test.go": srcATestBad},
			during: map[string]string{"a/a_test.go": srcATestRed}, changed: []string{"a/a_test.go"},
			check: "test", status: gate.StatusFail, agent: []string{"--- FAIL: TestA"},
			person: []string{"NOT COMPARED test", pkgA + " does not compile there"},
		},
		{
			name:   "a baseline with uncommitted changes is compared as it was",
			dirty:  map[string]string{"a/a_test.go": srcATestRed, "a/old_test.go": srcOldTest},
			during: map[string]string{"a/extra.go": srcExtra}, changed: []string{"a/extra.go"},
			check: "test", status: gate.StatusPreexisting, existing: []string{pkgA + " TestA", pkgA + " TestOld"},
			person: []string{"TestA", "TestOld"},
		},
		{
			name:   "what the session did to a file that was already dirty is the session's",
			dirty:  map[string]string{"a/a.go": srcA + "\n// Dirt is a comment.\n"},
			during: map[string]string{"a/a.go": srcAThree}, changed: []string{"a/a.go"},
			check: "test", status: gate.StatusFail, agent: []string{"--- FAIL: TestA"},
		},
	})
}

func TestRun_BuildFailuresTheBaselineHadDoNotBlock(t *testing.T) {
	t.Parallel()

	runComparisons(t, []comparison{
		{
			name:   "a package that did not compile before the session",
			base:   map[string]string{"lone/l.go": srcLoneBroken},
			during: map[string]string{"lone/extra.go": "package lone\n"}, changed: []string{"lone/extra.go"},
			check: "build", status: gate.StatusPreexisting, existing: []string{"lone/l.go:3:12: undefined: missing"},
			person: []string{"ALREADY FAILING", "build: lone/l.go:3:12: undefined: missing"},
		},
		{
			name:   "a test binary that did not compile before the session",
			base:   map[string]string{"a/a_test.go": srcATestBad},
			during: map[string]string{"a/extra.go": srcExtra}, changed: []string{"a/extra.go"},
			check: "build", status: gate.StatusPreexisting, existing: []string{"a/a_test.go:5:28: undefined: nope"},
			person: []string{"undefined: nope"},
		},
		{
			name:   "a build the session broke blocks",
			during: map[string]string{"a/a.go": srcABroken}, changed: []string{"a/a.go"},
			check: "build", status: gate.StatusFail, agent: []string{"FAIL build", "undefined: missing"},
		},
		{
			name:    "one new error beside an old one blocks whole",
			base:    map[string]string{"lone/l.go": srcLoneBroken},
			during:  map[string]string{"lone/extra.go": "package lone\n\nfunc M() { gone() }\n"},
			changed: []string{"lone/extra.go"}, check: "build", status: gate.StatusFail,
			agent: []string{"undefined: missing", "undefined: gone"},
		},
	})
}

func TestRun_AComparisonThatCannotRunStillBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
		base   gate.Baseline
		opts   []gate.Option
	}{
		{
			name: "a commit the repository does not have", reason: "invalid reference",
			base: gate.Baseline{Commit: strings.Repeat("0123", 10)},
		},
		{
			name: "a tree the repository does not have", reason: "NOT COMPARED test",
			base: gate.Baseline{Commit: "HEAD", Tree: strings.Repeat("0123", 10)},
		},
		{
			name: "no git", reason: "git not on PATH",
			base: gate.Baseline{Commit: "HEAD"}, opts: []gate.Option{without("git")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each case has its own repository: a worktree one case is
			// still removing is not another's leak.
			root := committed(t, module(map[string]string{"a/a_test.go": srcATestRed}))
			writeFile(t, root, "a/extra.go", srcExtra)

			ctx := gate.WithBaseline(t.Context(), tt.base)
			opts := slices.Concat(tt.opts, []gate.Option{gate.WithoutCache()})
			report := runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"}, opts...)

			assert.True(t, report.Failed(), "a failure nobody could compare is still a failure")
			assert.Contains(t, report.Render(), "--- FAIL: TestA")
			assert.NotContains(t, report.Render(), "NOT COMPARED", "the agent is not sent after the harness")
			assert.Contains(t, report.Notes(), "NOT COMPARED test: ")
			assert.Contains(t, report.Notes(), tt.reason)
			assert.Equal(t, 1, strings.Count(worktrees(t, root), "worktree "))
		})
	}
}

func TestRun_WithoutABaselineEveryFailureBlocks(t *testing.T) {
	t.Parallel()

	root := committed(t, module(map[string]string{"a/a_test.go": srcATestRed}))
	writeFile(t, root, "a/extra.go", srcExtra)

	for name, ctx := range map[string]context.Context{
		"no baseline":           t.Context(),
		"a baseline of nothing": gate.WithBaseline(t.Context(), gate.Baseline{}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"})

			assert.True(t, report.Failed())
			assert.Empty(t, report.Notes())
			assert.NoDirExists(t, filepath.Join(root, ".wavez", "cache"))
		})
	}
}

func TestRun_WhatTheBaselineSaidIsAskedOnce(t *testing.T) {
	t.Parallel()

	root := committed(t, module(map[string]string{"a/a_test.go": srcATestRed, "lone/l.go": srcLoneBroken}))
	ctx := began(t, root)
	writeFile(t, root, "a/extra.go", srcExtra)
	writeFile(t, root, "lone/extra.go", "package lone\n")

	cached := filepath.Join(root, ".wavez", "cache", "baseline")
	// Without git the baseline tree cannot be made, so an answer given
	// without it came from the cache.
	demoted := func(t *testing.T, changed, check string, opts ...gate.Option) gate.Result {
		t.Helper()

		return result(t, runGate(ctx, t, root, gate.StageStop, []string{changed}, opts...), check)
	}

	for _, tt := range []struct{ changed, check string }{{"a/extra.go", "test"}, {"lone/extra.go", "build"}} {
		assert.Equal(t, gate.StatusPreexisting, demoted(t, tt.changed, tt.check).Status)
		assert.Equal(t, gate.StatusPreexisting, demoted(t, tt.changed, tt.check, without("git")).Status,
			"the second stop does not build the tree again")

		off := demoted(t, tt.changed, tt.check, without("git"), gate.WithoutCache())
		assert.Equal(t, gate.StatusFail, off.Status, "the opt-out reads no cache")
		assert.Contains(t, off.Note, "git not on PATH")
	}

	entries, err := os.ReadDir(cached)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.NotContains(t, status(t, root), ".wavez", "the cache is not a change to the project")

	for _, entry := range entries {
		require.NoError(t, os.WriteFile(filepath.Join(cached, entry.Name()), []byte("{not json"), 0o600))
	}

	assert.Equal(t, gate.StatusFail, demoted(t, "a/extra.go", "test", without("git")).Status,
		"a corrupt entry is a miss")
	assert.Equal(t, gate.StatusPreexisting, demoted(t, "a/extra.go", "test").Status, "and is replaced by asking again")
	assert.Equal(t, gate.StatusPreexisting, demoted(t, "a/extra.go", "test", without("git")).Status)
}

// fixmeCheck fails on any selected file that holds FIXME, naming the line.
var fixmeCheck = check("fixme", `command = "! grep -Hn FIXME {files}"`)

func TestRun_CustomCheckFailuresTheBaselineHadDoNotBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		during  map[string]string
		name    string
		config  string
		status  gate.Status
		person  string
		changed []string
		opts    []gate.Option
	}{
		{
			name:   "the same lines as before the session",
			during: map[string]string{"a.txt": "FIXME one\nand a line the session added\n"}, changed: []string{"a.txt"},
			status: gate.StatusPreexisting,
			person: "ALREADY FAILING before this session, so it does not block: fixme: a.txt:1:FIXME one",
		},
		{
			name:   "a line the session added blocks",
			during: map[string]string{"a.txt": "FIXME one\nFIXME two\n"}, changed: []string{"a.txt"},
			status: gate.StatusFail,
		},
		{
			name:   "a file the baseline does not have is new",
			during: map[string]string{"new.txt": "FIXME one\n"}, changed: []string{"new.txt"},
			status: gate.StatusFail,
		},
		{
			name:   "a check that could not start at the baseline settles nothing",
			config: check("fixme", `command = "if test -e marker; then ! grep -Hn FIXME {files}; else exit 127; fi"`),
			during: map[string]string{"a.txt": "FIXME one\nmore\n", "marker": ""}, changed: []string{"a.txt"},
			status: gate.StatusFail,
		},
		{
			name: "a check that timed out at the baseline is the person's to hear about",
			config: check("fixme",
				"command = \"test -e marker || sleep 30; ! grep -Hn FIXME {files}\"\ntimeout = \"1s\""),
			during: map[string]string{"a.txt": "FIXME one\nmore\n", "marker": ""}, changed: []string{"a.txt"},
			status: gate.StatusFail,
			person: "NOT COMPARED fixme: could not ask the tree the session started from: timed out",
		},
		{
			name:   "without a shell there is nothing to ask",
			config: check("fixme", `command = "! grep -Hn FIXME {files}"`),
			during: map[string]string{"a.txt": "FIXME one\nmore\n"}, changed: []string{"a.txt"},
			status: gate.StatusFail, opts: []gate.Option{shellOnce()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := tt.config
			if config == "" {
				config = fixmeCheck
			}

			root := committed(t, map[string]string{".wavez.toml": config, "a.txt": "FIXME one\n"})
			ctx := began(t, root)

			for name, body := range tt.during {
				writeFile(t, root, name, body)
			}

			report := runGate(ctx, t, root, gate.StageStop, tt.changed, tt.opts...)

			assert.Equal(t, tt.status, report.Results[0].Status, "%+v", report.Results[0])
			assert.Equal(t, tt.status == gate.StatusFail, report.Failed())
			assert.Equal(t, tt.status == gate.StatusFail, report.Render() != "")

			if tt.person == "" {
				assert.Empty(t, report.Notes())
			} else {
				assert.Contains(t, report.Notes(), tt.person)
			}
		})
	}
}

// shellOnce finds sh for the check itself and not again for the baseline.
func shellOnce() gate.Option {
	var found atomic.Bool

	return gate.WithLookPath(func(name string) (string, error) {
		if name == "sh" && found.Swap(true) {
			return "", errNotInstalled
		}

		return exec.LookPath(name)
	})
}

// counted is a check that appends a line to runs.log each time it runs, and
// passes unless a selected file holds "bad".
func counted(command string) string {
	return check("counted", `command = "echo ran >> runs.log; `+command+`"`)
}

func runs(t *testing.T, root string) int {
	t.Helper()

	data, err := fs.ReadFile(os.DirFS(root), "runs.log")
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}

	require.NoError(t, err)

	return strings.Count(string(data), "\n")
}

func TestRun_CustomCheckRunsAgain_AfterAFileItWasNotHandedChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		// between happens after the first run, which passes.
		between map[string]string
		status  gate.Status
		want    int
	}{
		{
			name: "the script the command runs was edited to fail", command: "sh ./check.sh {files}",
			between: map[string]string{"check.sh": "exit 1\n"}, status: gate.StatusFail, want: 2,
		},
		{
			name: "nothing changed and it is still run", command: "sh ./check.sh {files}",
			status: gate.StatusPass, want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, map[string]string{
				".wavez.toml": counted(tt.command), "check.sh": "exit 0\n", "a.txt": "good\n",
			})

			expect(t, runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"}),
				want{"counted", gate.StatusPass, ""})

			for name, body := range tt.between {
				writeFile(t, root, name, body)
			}

			report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"})

			expect(t, report, want{"counted", tt.status, ""})
			assert.Equal(t, tt.want, runs(t, root),
				"a pass on the handed files is not a pass on what the command reads")
		})
	}
}

func TestRun_TheCacheIgnoresItselfAndHonorsTheEnvironment(t *testing.T) {
	root := committed(t, module(map[string]string{"a/a_test.go": srcATestRed}))
	ctx := began(t, root)
	writeFile(t, root, "a/extra.go", srcExtra)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".wavez")))

	t.Setenv(gate.NoCacheEnv, "1")

	off := result(t, runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"}), "test")
	assert.Equal(t, gate.StatusPreexisting, off.Status)
	assert.NoDirExists(t, filepath.Join(root, ".wavez"))

	t.Setenv(gate.NoCacheEnv, "0")

	on := result(t, runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"}), "test")
	assert.Equal(t, gate.StatusPreexisting, on.Status)
	assert.DirExists(t, filepath.Join(root, ".wavez", "cache", "baseline"))
	assert.Equal(t, "?? a/extra.go\n", status(t, root), "a project that never ran init does not see the cache")
}
