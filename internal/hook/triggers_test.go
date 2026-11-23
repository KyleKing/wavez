package hook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
)

// notChecked is a run in which nothing failed and one check could not
// finish, which is what a check that hit its timeout looks like.
var notChecked = hook.Verdict{Abstained: true, Notes: "NOT CHECKED test: timed out after 1m30s\n"}

func TestHandle_Stop_ACheckThatTimedOutIsNotAPass(t *testing.T) {
	t.Parallel()

	root := repo(t)
	runner := &fakeRunner{verdicts: []hook.Verdict{notChecked}}
	h := hook.New(runner)

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	first, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Empty(t, first.Decision, "a check that could not run never blocks")
	assert.Equal(t, "wavez: NOT CHECKED test: timed out after 1m30s\n", first.SystemMessage)
	assert.Empty(t, first.Specific.AdditionalContext, "and the agent hears nothing of it")

	st := state(t, root)
	assert.Empty(t, st.Passed, "the change set was not checked, so it did not pass")
	assert.NotEmpty(t, st.Abstained)

	second, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Empty(t, second.raw, "the person was told once")
	assert.Len(t, runner.calls, 1, "the same bytes are not put through the same timeout again")

	// Any further change is a new change set, and the checks get their
	// chance at it: this is what recording a pass used to prevent.
	write(t, root, "a.go", "package a // changed again\n")

	runner.verdicts = []hook.Verdict{red}

	third, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Equal(t, "block", third.Decision)
	assert.Len(t, runner.calls, 2)
}

// interrupted is a runner whose run is cut short the way SIGTERM cuts one:
// the context is canceled under it, and what comes back is what the checks
// had by then.
type interrupted struct {
	cancel  context.CancelFunc
	verdict hook.Verdict
	runs    int
}

func (r *interrupted) Run(context.Context, string, []string, gate.Stage) (hook.Verdict, error) {
	r.runs++

	if r.cancel != nil {
		r.cancel()
	}

	return r.verdict, nil
}

func TestHandle_Stop_AnInterruptedRunRecordsNothingAndRunsAgain(t *testing.T) {
	t.Parallel()

	root := repo(t)
	ctx, cancel := context.WithCancel(t.Context())
	runner := &interrupted{
		cancel: cancel, verdict: hook.Verdict{Abstained: true, Notes: "NOT CHECKED slow: canceled\n"},
	}
	h := hook.New(runner)

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	in, err := json.Marshal(stop(root, false))
	require.NoError(t, err)

	var out bytes.Buffer

	require.NoError(t, h.Handle(ctx, bytes.NewReader(in), &out))
	assert.Empty(t, out.String())

	st := state(t, root)
	assert.Empty(t, st.Passed, "an interrupted run is not a pass")
	assert.Empty(t, st.Abstained, "and is not a settled abstention either")

	// The next stop, uninterrupted, runs the check that never finished.
	runner.cancel, runner.verdict = nil, red

	res, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Equal(t, "block", res.Decision)
	assert.Equal(t, 2, runner.runs)
}

func TestHandle_PostToolUse_RelativePathIsReadFromTheEventsCwd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  string
		path string
		want string
	}{
		{name: "the agent is in a subdirectory", cwd: "sub", path: "b.go", want: "sub/b.go"},
		{name: "the agent is at the root", cwd: ".", path: "sub/b.go", want: "sub/b.go"},
		{name: "a path that climbs back up", cwd: "sub", path: "../a.go", want: "a.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			runner := &fakeRunner{}
			h := hook.New(runner)

			ev := edit(root, "Edit", tt.path)
			ev["cwd"] = filepath.Join(root, tt.cwd)

			_, err := fire(t, h, ev)
			require.NoError(t, err)

			require.Len(t, runner.calls, 1)
			assert.Equal(t, []string{tt.want}, runner.calls[0].changed)
			assert.Equal(t, []string{tt.want}, state(t, root).Recorded)
		})
	}
}

func TestResolveRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	write(t, base, "repo/.git/HEAD", "")
	write(t, base, "repo/a.go", "package a\n")
	write(t, base, "repo/tools/go.mod", "module tools\n")
	write(t, base, "repo/tools/t.go", "package tools\n")
	write(t, base, "mono/.git/HEAD", "")
	write(t, base, "mono/svc/go.mod", "module svc\n")
	write(t, base, "mono/svc/internal/x/x.go", "package x\n")
	write(t, base, "configured/.wavez.toml", "")
	write(t, base, "configured/web/package.json", "{}\n")
	write(t, base, "bare/notes.txt", "x\n")

	at := func(rel string) string {
		if rel == "" {
			return ""
		}

		return filepath.Join(base, filepath.FromSlash(rel))
	}

	tests := []struct {
		name       string
		projectDir string
		cwd        string
		want       string
	}{
		{name: "the agent cd'd into a nested module", projectDir: "repo", cwd: "repo/tools", want: "repo"},
		{name: "the same cd, on a harness that names no project", cwd: "repo/tools", want: "repo"},
		{
			name:       "a session started inside a module of a larger repository",
			projectDir: "mono/svc", cwd: "mono/svc/internal/x", want: "mono/svc",
		},
		{
			name:       "a configured root above where the session started",
			projectDir: "configured/web", cwd: "configured/web", want: "configured",
		},
		{
			name:       "a project directory that is gone falls back to the cwd",
			projectDir: "nowhere", cwd: "repo", want: "repo",
		},
		{
			name:       "a session started outside any project that cd'd into one",
			projectDir: "bare", cwd: "repo/tools", want: "repo",
		},
		{name: "a cwd that does not exist", projectDir: "repo", cwd: "repo/gone"},
		{name: "a directory with no marker above it", cwd: "bare"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, ok := hook.ResolveRoot(at(tt.projectDir), at(tt.cwd))
			assert.Equal(t, at(tt.want), root)
			assert.Equal(t, tt.want != "", ok)
		})
	}
}

func TestHandle_Stop_CwdBelowTheRootDoesNotStartASecondSession(t *testing.T) {
	t.Parallel()

	root := repo(t)
	write(t, root, "tools/go.mod", "module tools\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "tools")

	runner := &fakeRunner{verdicts: []hook.Verdict{red}}
	h := hook.New(runner, hook.WithProjectDir(root))

	start(t, h, root)
	write(t, root, "a.go", "package a // broken by the session\n")

	// The agent has run `cd tools` by the time it stops.
	ev := stop(root, false)
	ev["cwd"] = filepath.Join(root, "tools")

	res, err := fire(t, h, ev)
	require.NoError(t, err)

	assert.Equal(t, "block", res.Decision, "the stop still sees what the session changed above the nested module")
	require.Len(t, runner.calls, 1)
	assert.Equal(t, []string{"a.go"}, runner.calls[0].changed)
	assert.NoDirExists(t, filepath.Join(root, "tools", ".wavez"))
}

func TestHandle_EventFromNowhereIsSilentAndWritesNothing(t *testing.T) {
	t.Parallel()

	bare := t.TempDir()

	tests := []struct {
		name string
		cwd  string
	}{
		{name: "a cwd that does not exist", cwd: filepath.Join(bare, "nonexistent", "zzz")},
		{name: "a directory that is not a project", cwd: bare},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var trace bytes.Buffer

			runner := &fakeRunner{verdicts: []hook.Verdict{red}}
			h := hook.New(runner, hook.WithTrace(&trace), hook.WithLookPath(found))

			for _, ev := range []map[string]any{
				{"session_id": "x", "cwd": tt.cwd, "hook_event_name": "SessionStart"},
				edit(tt.cwd, "Edit", "a.go"),
				shell(tt.cwd, "x", `sed -i 's/Read/ReadLog/g' a.go`),
				stop(tt.cwd, false),
			} {
				res, err := fire(t, h, ev)
				require.NoError(t, err)
				assert.Empty(t, res.raw)
			}

			assert.Empty(t, runner.calls)
			assert.Equal(t, 4, strings.Count(trace.String(), `"note":"no project at `))

			entries, err := os.ReadDir(bare)
			require.NoError(t, err)
			assert.Empty(t, entries, "no .wavez, and no directory made on the way to one")
		})
	}
}

func TestHandle_ProjectThatNeverRanInitIgnoresItsOwnState(t *testing.T) {
	t.Parallel()

	// Hooks installed in ~/.claude/settings.json fire in every project,
	// and a project with a marker gets its state whether or not init ran
	// there. What it must not get is that state in its git status.
	root := repo(t)
	h := hook.New(&fakeRunner{})

	start(t, h, root)

	_, err := fire(t, h, shell(root, "s", "ls"))
	require.NoError(t, err)

	assert.Equal(t, "*\n", read(t, root, ".wavez/.gitignore"))
}

func TestAnnouncement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		want     string
		builtins []string
		custom   []string
	}{
		{
			name:     "a Go module with a linter",
			builtins: []string{"format", "build", "test", "lint"},
			want: "Format, build, tests and lint for the packages you change run automatically when you finish; " +
				"do not run whole-project build, test or lint commands yourself.",
		},
		{
			name:     "a Go module with no linter configured",
			builtins: []string{"format", "build", "test"},
			want: "Format, build and tests for the packages you change run automatically when you finish; " +
				"do not run whole-project build or test commands yourself.",
		},
		{
			name:     "tests turned off",
			builtins: []string{"format", "build"},
			want: "Format and build for the packages you change run automatically when you finish; " +
				"do not run whole-project build commands yourself.",
		},
		{
			name:     "only the formatter",
			builtins: []string{"format"},
			want: "Format for the packages you change runs automatically when you finish; " +
				"do not run them yourself.",
		},
		{
			name:   "another language's own checks",
			custom: []string{"ruff", "mypy"},
			want: "Ruff and mypy for the files you change run automatically when you finish; " +
				"do not run them yourself.",
		},
		{
			name:     "built-ins beside more checks than are worth naming",
			builtins: []string{"build"}, custom: []string{"a", "b", "c", "d", "e"},
			want: "Build, a, b, c and 2 other checks for the packages you change run automatically when you finish; " +
				"do not run whole-project build commands yourself.",
		},
		{name: "a project with no checks is told nothing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := hook.AnnouncementFor(tt.builtins, tt.custom)
			assert.Equal(t, tt.want, got)

			// About 35 tokens: the agent pays for this on every turn.
			assert.LessOrEqual(t, len(strings.Fields(got)), 27)
			assert.LessOrEqual(t, len(got), 180)
		})
	}
}

func TestHandle_SessionStart_Announces(t *testing.T) {
	t.Parallel()

	goChecks := func(string) ([]string, []string, error) { return []string{"format", "build", "test"}, nil, nil }
	noChecks := func(string) ([]string, []string, error) { return nil, nil, nil }
	broken := func(string) ([]string, []string, error) { return nil, nil, errRunner }

	const sentence = "Format, build and tests for the packages you change run automatically when you finish; " +
		"do not run whole-project build or test commands yourself."

	tests := []struct {
		name   string
		source string
		want   string
		opts   []hook.Option
	}{
		{name: "a new session", source: "startup", opts: []hook.Option{hook.WithChecks(goChecks)}, want: sentence},
		{name: "after /clear", source: "clear", opts: []hook.Option{hook.WithChecks(goChecks)}, want: sentence},
		{
			name:   "after compaction dropped it",
			source: "compact", opts: []hook.Option{hook.WithChecks(goChecks)}, want: sentence,
		},
		{name: "a harness that names no source", opts: []hook.Option{hook.WithChecks(goChecks)}, want: sentence},
		{name: "a resumed session already has it", source: "resume", opts: []hook.Option{hook.WithChecks(goChecks)}},
		{
			name:   "turned off for one arm of a benchmark",
			source: "startup", opts: []hook.Option{hook.WithChecks(goChecks), hook.WithoutAnnouncement()},
		},
		{name: "a project with no checks", source: "startup", opts: []hook.Option{hook.WithChecks(noChecks)}},
		{name: "a configuration that does not load", source: "startup", opts: []hook.Option{hook.WithChecks(broken)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)

			var trace bytes.Buffer

			h := hook.New(&fakeRunner{}, append(tt.opts, hook.WithTrace(&trace))...)

			res, err := fire(t, h, map[string]any{
				"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart", "source": tt.source,
			})
			require.NoError(t, err)

			assert.FileExists(t, filepath.Join(root, ".wavez", "sessions", sessionID+".json"),
				"the baseline is taken either way")

			tally, err := hook.ReadTally(bytes.NewReader(trace.Bytes()))
			require.NoError(t, err)

			if tt.want == "" {
				assert.Empty(t, res.raw)
				assert.Equal(t, hook.Tally{}, tally)

				return
			}

			assert.Equal(t, "SessionStart", res.Specific.EventName)
			assert.Equal(t, tt.want, res.Specific.AdditionalContext)
			assert.ElementsMatch(t, []string{"hookEventName", "additionalContext"}, res.specificKeys)
			assert.Equal(t, hook.Tally{Announcements: 1, HookTextBytes: len(tt.want)}, tally)
			assert.Contains(t, trace.String(), `"note":"announced the gates"`)
		})
	}
}

func TestReadTally_TotalsEveryTextTheAgentWasHanded(t *testing.T) {
	t.Parallel()

	root := repo(t)

	var trace bytes.Buffer

	runner := &fakeRunner{verdicts: []hook.Verdict{{Text: "REWRITTEN a.go\n"}}}
	h := hook.New(runner, hook.WithTrace(&trace),
		hook.WithChecks(func(string) ([]string, []string, error) { return []string{"build"}, nil, nil }))

	handed := 0
	send := func(ev map[string]any) result {
		res, err := fire(t, h, ev)
		require.NoError(t, err)

		handed += len(res.Reason) + len(res.Specific.AdditionalContext) + len(res.Specific.PermissionReason)

		return res
	}

	send(map[string]any{"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart"})
	send(edit(root, "Edit", filepath.Join(root, "a.go")))

	write(t, root, "a.go", "package a // changed\n")

	runner.verdicts = []hook.Verdict{red}
	assert.Equal(t, "block", send(stop(root, false)).Decision)

	runner.verdicts = []hook.Verdict{{Notes: "NOT CHECKED lint: golangci-lint not on PATH\n", Abstained: true}}
	write(t, root, "a.go", "package a // fixed\n")

	quiet := send(stop(root, true))
	assert.NotEmpty(t, quiet.SystemMessage)

	tally, err := hook.ReadTally(bytes.NewReader(trace.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, hook.Tally{Announcements: 1, StopBlocks: 1, HookTextBytes: handed}, tally)
	assert.Positive(t, handed)

	lines := strings.Split(strings.TrimSpace(trace.String()), "\n")
	assert.Contains(t, lines[len(lines)-1], `"outcome":"notice"`,
		"a note for the person on a stop that was let through is not a release, and costs the agent nothing")
	assert.NotContains(t, lines[len(lines)-1], "agent_bytes")
}
