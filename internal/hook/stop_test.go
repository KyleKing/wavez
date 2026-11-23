package hook_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
)

var (
	red   = hook.Verdict{Failed: true, Fingerprint: "vet:a.go", Text: "FAIL vet\na.go:3: unreachable code"}
	green = hook.Verdict{}
)

// step is one Stop event in a scenario: what happens to the tree first, the
// verdict the gates would give, and what the hook must do.
type step struct {
	// touch rewrites a.go with this content before the stop, when set.
	touch string
	// want is block, released, context, or silent.
	want    string
	verdict hook.Verdict
	active  bool
	// ran says whether the gates were run at all.
	ran bool
}

// turns is a session told as the stops it made.
type turns struct {
	name      string
	steps     []step
	maxRounds int
}

func runTurns(t *testing.T, tests []turns) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			runner := &fakeRunner{}
			h := hook.New(runner, hook.WithMaxRounds(tt.maxRounds))
			start(t, h, root)

			for i, s := range tt.steps {
				if s.touch != "" {
					write(t, root, "a.go", "package a // "+s.touch+"\n")
				}

				runner.verdicts = []hook.Verdict{s.verdict}
				runner.calls = nil

				res, err := fire(t, h, stop(root, s.active))
				require.NoError(t, err, "step %d", i)
				assert.Equal(t, s.want, outcome(res), "step %d", i)

				if s.ran {
					want := []call{{stage: gate.StageStop, changed: []string{"a.go"}}}
					assert.Equal(t, want, runner.calls, "step %d", i)
				} else {
					assert.Empty(t, runner.calls, "step %d", i)
				}
			}
		})
	}
}

// start fires SessionStart, so that what a test changes afterwards is the
// session's change and not dirt the session found.
func start(t *testing.T, h *hook.Handler, root string) {
	t.Helper()

	_, err := fire(t, h, map[string]any{"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart"})
	require.NoError(t, err)
}

func outcome(res result) string {
	switch {
	case res.Decision == "block":
		return "block"
	case res.SystemMessage != "":
		return "released"
	case res.Specific.AdditionalContext != "":
		return "context"
	case res.raw == "":
		return "silent"
	default:
		return "unexpected: " + res.raw
	}
}

func TestHandle_Stop_Passing(t *testing.T) {
	t.Parallel()

	advisory := hook.Verdict{Text: "ADVISORY todo (does not block)\na.go:5: TODO"}

	runTurns(t, []turns{
		{
			name:  "nothing changed runs nothing",
			steps: []step{{want: "silent"}, {want: "silent"}},
		},
		{
			name: "a pass is silent and the same change set is not checked twice",
			steps: []step{
				{touch: "one", verdict: green, want: "silent", ran: true},
				{verdict: red, want: "silent"},
				{active: true, verdict: red, want: "silent"},
			},
		},
		{
			name: "a further change after a pass is checked again",
			steps: []step{
				{touch: "one", verdict: green, want: "silent", ran: true},
				{touch: "two", verdict: red, want: "block", ran: true},
				{touch: "three", active: true, verdict: green, want: "silent", ran: true},
				{touch: "three", verdict: red, want: "silent"},
			},
		},
		{
			name: "non-blocking text is surfaced once and passes",
			steps: []step{
				{touch: "one", verdict: advisory, want: "context", ran: true},
				{active: true, verdict: advisory, want: "silent"},
				{touch: "two", verdict: advisory, want: "silent", ran: true},
			},
		},
		{
			name: "fixing the failure passes and clears the count",
			steps: []step{
				{touch: "one", verdict: red, want: "block", ran: true},
				{touch: "two", active: true, verdict: red, want: "block", ran: true},
				{touch: "fixed", active: true, verdict: green, want: "silent", ran: true},
				{touch: "broken again", verdict: red, want: "block", ran: true},
			},
		},
	})
}

func TestHandle_Stop_Failing(t *testing.T) {
	t.Parallel()

	other := hook.Verdict{Failed: true, Fingerprint: "test:TestX", Text: "FAIL TestX"}

	runTurns(t, []turns{
		{
			name: "the same failure blocks until the round cap, then the user is told",
			steps: []step{
				{touch: "one", verdict: red, want: "block", ran: true},
				{touch: "two", active: true, verdict: red, want: "block", ran: true},
				{touch: "three", active: true, verdict: red, want: "released", ran: true},
			},
		},
		{
			name: "the cap holds without any edit between rounds",
			steps: []step{
				{touch: "one", verdict: red, want: "block", ran: true},
				{active: true, verdict: red, want: "block", ran: true},
				{active: true, verdict: red, want: "released", ran: true},
				{verdict: red, want: "silent"},
				{active: true, verdict: red, want: "silent"},
			},
		},
		{
			name: "a released failure never blocks a later turn",
			steps: []step{
				{touch: "one", verdict: red, want: "block", ran: true},
				{active: true, verdict: red, want: "block", ran: true},
				{active: true, verdict: red, want: "released", ran: true},
				{touch: "next turn", verdict: red, want: "released", ran: true},
				{touch: "and another", verdict: red, want: "released", ran: true},
			},
		},
		{
			name: "stop_hook_active does not let a second round through",
			steps: []step{
				{touch: "one", verdict: red, want: "block", ran: true},
				{touch: "two", active: true, verdict: red, want: "block", ran: true},
			},
		},
		{
			name: "a different failure starts its own count",
			steps: []step{
				{touch: "one", verdict: red, want: "block", ran: true},
				{touch: "two", active: true, verdict: red, want: "block", ran: true},
				{touch: "three", active: true, verdict: other, want: "block", ran: true},
				{touch: "four", active: true, verdict: other, want: "block", ran: true},
				{touch: "five", active: true, verdict: other, want: "released", ran: true},
			},
		},
		{
			name:      "a round cap of one never blocks",
			maxRounds: 1,
			steps:     []step{{touch: "one", verdict: red, want: "released", ran: true}},
		},
		{
			name: "a failure without a fingerprint or text still counts",
			steps: []step{
				{touch: "one", verdict: hook.Verdict{Failed: true}, want: "block", ran: true},
				{active: true, verdict: hook.Verdict{Failed: true}, want: "block", ran: true},
				{active: true, verdict: hook.Verdict{Failed: true}, want: "released", ran: true},
			},
		},
	})
}

func TestHandle_Stop_ACheckThatCouldNotRunIsToldToThePersonOnly(t *testing.T) {
	t.Parallel()

	root := repo(t)
	unchecked := hook.Verdict{Notes: "NOT CHECKED lint: golangci-lint not on PATH\n"}
	h := hook.New(&fakeRunner{verdicts: []hook.Verdict{unchecked, unchecked}})

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	first, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Empty(t, first.Decision)
	assert.Empty(t, first.Specific.AdditionalContext, "an agent told a tool is missing goes looking for it")
	assert.Contains(t, first.SystemMessage, "NOT CHECKED lint")

	write(t, root, "a.go", "package a // changed again\n")

	second, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Empty(t, second.SystemMessage, "the same note is not repeated")
}

func TestHandle_Stop_Output(t *testing.T) {
	t.Parallel()

	root := repo(t)
	h := hook.New(&fakeRunner{verdicts: []hook.Verdict{red}}, hook.WithMaxRounds(2))

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	blocked, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.JSONEq(t, `{"decision":"block","reason":"FAIL vet\na.go:3: unreachable code"}`, blocked.raw)

	released, err := fire(t, h, stop(root, true))
	require.NoError(t, err)
	assert.Empty(t, released.Decision)
	assert.Empty(t, released.Specific.AdditionalContext, "the model is not told; the user is")
	assert.Contains(t, released.SystemMessage, "2 times in a row")
	assert.Contains(t, released.SystemMessage, "a.go:3: unreachable code")

	st := state(t, root)
	assert.Equal(t, "vet:a.go", st.Failure.Fingerprint)
	assert.Equal(t, 2, st.Failure.Rounds)
	assert.Empty(t, st.Passed)
}

func TestHandle_Stop_SeesWhatNoEditToolWrote(t *testing.T) {
	t.Parallel()

	root := repo(t)
	write(t, root, "dirty.txt", "the user's own work in progress\n")

	runner := &fakeRunner{verdicts: []hook.Verdict{green}}
	h := hook.New(runner)

	start(t, h, root)

	// A shell command, a generator, and `wavez rename` all look like this:
	// files change and no PostToolUse event names them.
	write(t, root, "sub/b.go", "package sub // sed -i\n")
	write(t, root, "gen/zz_generated.go", "package gen\n")
	require.NoError(t, os.Remove(filepath.Join(root, "a.go")))

	_, err := fire(t, h, edit(root, "Write", filepath.Join(root, "new.go")))
	require.NoError(t, err)

	runner.calls = nil

	_, err = fire(t, h, stop(filepath.Join(root, "sub"), false))
	require.NoError(t, err)
	assert.Equal(t, []call{{
		stage:   gate.StageStop,
		changed: []string{"a.go", "gen/zz_generated.go", "new.go", "sub/b.go"},
	}}, runner.calls, "the user's dirty.txt predates the session and is not the session's change")
}

func TestHandle_Stop_FixerRewritesDuringTheRun(t *testing.T) {
	t.Parallel()

	root := repo(t)
	runner := &fakeRunner{verdicts: []hook.Verdict{green}}
	runner.onRun = func() { write(t, root, "a.go", "package a // formatted\n") }

	h := hook.New(runner)

	start(t, h, root)
	write(t, root, "a.go", "package a //unformatted\n")

	_, err := fire(t, h, stop(root, false))
	require.NoError(t, err)

	runner.onRun = nil

	_, err = fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Len(t, runner.calls, 1, "the pass covers the tree the fixer left, so it is not run again")
}

func TestHandle_Stop_OutsideGit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n")
	write(t, root, "pkg/a.go", "package pkg\n")

	runner := &fakeRunner{verdicts: []hook.Verdict{red}}
	h := hook.New(runner)

	res, err := fire(t, h, stop(filepath.Join(root, "pkg"), false))
	require.NoError(t, err)
	assert.Empty(t, res.raw, "with no edit recorded there is nothing to check")

	_, err = fire(t, h, edit(root, "Edit", filepath.Join(root, "pkg", "a.go")))
	require.NoError(t, err)

	res, err = fire(t, h, stop(filepath.Join(root, "pkg"), false))
	require.NoError(t, err)
	assert.Equal(t, "block", res.Decision)
	assert.Equal(t, []string{"pkg/a.go"}, runner.calls[len(runner.calls)-1].changed)
}

func TestHandle_Stop_OneTurnIsBoundedHoweverTheFailureVaries(t *testing.T) {
	t.Parallel()

	root := repo(t)
	runner := &fakeRunner{}
	h := hook.New(runner, hook.WithMaxRounds(2))

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	outcomes := make([]string, 0, 9)

	for i := range cap(outcomes) {
		id := strconv.Itoa(i)
		runner.verdicts = []hook.Verdict{{Failed: true, Fingerprint: id, Text: "failure " + id}}

		res, err := fire(t, h, stop(root, i > 0))
		require.NoError(t, err)

		outcomes = append(outcomes, outcome(res))
	}

	// Five blocks and then a release on the sixth stop: under the eight
	// consecutive continuations after which the harness would end the turn
	// itself, without a word to the person.
	want := slices.Concat(slices.Repeat([]string{"block"}, 5), []string{"released", "silent", "silent", "silent"})
	assert.Equal(t, want, outcomes)
	assert.Len(t, runner.stages(), 6, "a change set that was given up on is not run again")
}

func TestHandle_Stop_LongTextIsClipped(t *testing.T) {
	t.Parallel()

	root := repo(t)
	long := strings.Repeat("é", 20000)
	h := hook.New(&fakeRunner{verdicts: []hook.Verdict{{Text: long}}})

	start(t, h, root)
	write(t, root, "a.go", "package a // changed\n")

	res, err := fire(t, h, stop(root, false))
	require.NoError(t, err)
	assert.Equal(t, "Stop", res.Specific.EventName)
	assert.Less(t, len([]rune(res.Specific.AdditionalContext)), 10000)
	assert.Contains(t, res.Specific.AdditionalContext, "truncated")
}
