package hook_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/route"
	"github.com/kyleking/wavez/internal/session"
)

var errModel = errors.New("model is down")

// model is a decision backend that gives one answer, or fails, and counts
// how often it was asked.
type model struct {
	err   error
	key   string
	asked int
	// wait makes the model slow until the context ends.
	wait bool
}

func (*model) Name() string { return "fake" }

func (m *model) Answer(ctx context.Context, _ decide.Question, _ string) (decide.Answer, error) {
	m.asked++

	if m.wait {
		<-ctx.Done()

		return decide.Answer{}, fmt.Errorf("waiting on the model: %w", ctx.Err())
	}

	return decide.Answer{Key: m.key}, m.err
}

func prompt(root, text string) map[string]any {
	return map[string]any{
		"session_id": sessionID, "cwd": root, "hook_event_name": "UserPromptSubmit",
		"prompt": text, "permission_mode": "default", "transcript_path": "/x.jsonl",
	}
}

func TestHandle_UserPromptSubmit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model *model
		name  string
		text  string
		// want is the route whose hint is expected, "" for silence.
		want string
		// off leaves the route hint turned off.
		off       bool
		wantAsked int
	}{
		{
			name: "a rule answers and the model is not asked", text: "Rename Read in a.go to ReadLog.",
			model: &model{key: route.Explore}, want: route.Direct,
		},
		{
			name: "no rule fires and the model answers", text: "Make startup faster.",
			model: &model{key: route.Explore}, want: route.Explore, wantAsked: 1,
		},
		{
			name: "the model picks coordinate", text: "Tidy the docs and the CI config.",
			model: &model{key: route.Coordinate}, want: route.Coordinate, wantAsked: 1,
		},
		{name: "the model picks the fallback", text: "thanks", model: &model{key: route.Silent}, wantAsked: 1},
		{name: "the model answers outside the set", text: "thanks", model: &model{key: "rm -rf"}, wantAsked: 1},
		{name: "the model fails", text: "Make startup faster.", model: &model{err: errModel}, wantAsked: 1},
		{name: "no model and no rule", text: "Make startup faster."},
		{name: "an empty prompt", text: "", model: &model{key: route.Explore}},
		{
			name: "the hint is off", text: "Rename Read in a.go to ReadLog.",
			model: &model{key: route.Explore}, off: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)

			var (
				opts  []hook.Option
				trace bytes.Buffer
			)

			decider := decide.New()
			if tt.model != nil {
				decider = decide.New(decide.WithBackend(tt.model))
			}

			if !tt.off {
				opts = append(opts, hook.WithRoute(decider))
			}

			res, err := fire(t, hook.New(&fakeRunner{}, append(opts, hook.WithTrace(&trace))...), prompt(root, tt.text))
			require.NoError(t, err, "a prompt is never failed")

			if tt.model != nil {
				assert.Equal(t, tt.wantAsked, tt.model.asked)
			}

			assert.NoDirExists(t, root+"/.wavez/sessions", "routing a prompt takes no baseline")

			if tt.want == "" {
				assert.Empty(t, res.raw, "the fallback is no output at all")
				assert.Contains(t, trace.String(), `"outcome":"silent"`)

				return
			}

			assert.Equal(t, "UserPromptSubmit", res.Specific.EventName)
			assert.Equal(t, route.Hint(tt.want), res.Specific.AdditionalContext)
			assert.ElementsMatch(t, []string{"hookEventName", "additionalContext"}, res.specificKeys)
			assert.Empty(t, res.Decision, "a hint never blocks the prompt")
			assert.Contains(t, trace.String(), `"note":"route `+tt.want+` by `)
		})
	}
}

func TestHandle_UserPromptSubmit_ASlowModelCostsABoundedWait(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	slow := &model{wait: true}
	handler := hook.New(&fakeRunner{}, hook.WithRoute(decide.New(decide.WithBackend(slow))))

	var out bytes.Buffer

	in := bytes.NewBufferString(`{"session_id":"s","cwd":"` + module(t) +
		`","hook_event_name":"UserPromptSubmit","prompt":"Make startup faster."}`)

	require.NoError(t, handler.Handle(ctx, in, &out))
	assert.Empty(t, out.String())
	assert.Equal(t, 1, slow.asked)
}

func TestReadTally(t *testing.T) {
	t.Parallel()

	root := module(t)

	var trace bytes.Buffer

	handler := hook.New(&fakeRunner{}, hook.WithTrace(&trace), hook.WithRoute(decide.New()), hook.WithLookPath(found))
	handed := 0

	for _, ev := range []map[string]any{
		prompt(root, "Rename Read in internal/bench/stats.go to ReadLog."),
		prompt(root, "thanks"),
		shell(root, "tally", `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`),
		shell(root, "tally", `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`),
		shell(root, "tally", `ls`),
		stop(root, false),
	} {
		res, err := fire(t, handler, ev)
		require.NoError(t, err)

		handed += len(res.Reason) + len(res.Specific.AdditionalContext) + len(res.Specific.PermissionReason)
	}

	trace.WriteString("not json\n")

	tally, err := hook.ReadTally(&trace)
	require.NoError(t, err)
	require.Positive(t, handed)
	assert.Equal(t, hook.Tally{RouteHints: 1, RenameDenials: 1, HookTextBytes: handed}, tally,
		"the bytes are every text an event handed the agent, summed")

	_, err = hook.ReadTally(failingReader{})
	require.ErrorIs(t, err, errRead)
}

func TestReadSessionTally(t *testing.T) {
	t.Parallel()

	lines := `{"session":"a","event":"UserPromptSubmit","outcome":"context","agent_bytes":10}
{"session":"b","event":"UserPromptSubmit","outcome":"context","agent_bytes":20}
{"session":"a","event":"Stop","outcome":"block","agent_bytes":5}
not json
{"event":"Stop","outcome":"block","agent_bytes":99}
`

	tests := []struct {
		name    string
		session string
		want    hook.Tally
	}{
		{"one session", "a", hook.Tally{RouteHints: 1, StopBlocks: 1, HookTextBytes: 15}},
		{"the other", "b", hook.Tally{RouteHints: 1, HookTextBytes: 20}},
		{"a session with no lines", "c", hook.Tally{}},
		{"no session counts every line", "", hook.Tally{RouteHints: 2, StopBlocks: 2, HookTextBytes: 134}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := hook.ReadSessionTally(strings.NewReader(lines), tt.session)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	_, err := hook.ReadSessionTally(failingReader{}, "a")
	require.ErrorIs(t, err, errRead)
}

var errRead = errors.New("disk gone")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errRead }

func TestHandle_UserPromptSubmit_SaysWhereANamedSymbolIs(t *testing.T) {
	t.Parallel()

	root := repo(t)
	write(t, root, "sub/parse.go", "package sub\n\n// ParseThing parses.\nfunc ParseThing() {}\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "parse")

	tests := []struct {
		name   string
		prompt string
		want   string
		opts   []hook.Option
	}{
		{
			name: "a name the repository declares", opts: []hook.Option{hook.WithLocate()},
			prompt: "ParseThing drops its argument",
			want: "wavez looked these up, so you need not search for them:\n" +
				"ParseThing is declared at sub/parse.go:4\nTo check: go test ./sub/",
		},
		{name: "a prompt that names nothing", opts: []hook.Option{hook.WithLocate()}, prompt: "tidy the readme"},
		{name: "the lookup is off", prompt: "ParseThing drops its argument"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := fire(t, hook.New(&fakeRunner{}, tt.opts...), prompt(root, tt.prompt))
			require.NoError(t, err)
			assert.Equal(t, tt.want, res.Specific.AdditionalContext)
		})
	}
}

func TestHandle_SessionStart_WithTheBriefNamesWavezApply(t *testing.T) {
	t.Parallel()

	root := repo(t)
	handler := hook.New(&fakeRunner{}, hook.WithBrief(), hook.WithoutStopChecks())

	res, err := fire(t, handler, map[string]any{
		"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart", "source": "startup",
	})
	require.NoError(t, err)
	assert.Contains(t, res.Specific.AdditionalContext, "Make edits with `wavez apply`")
	assert.NotContains(t, res.Specific.AdditionalContext, "wavez check")
}

func TestHandle_SessionStart_AfterACompactionSaysWhatTheSessionHad(t *testing.T) {
	t.Parallel()

	root := repo(t)
	handler := hook.New(&fakeRunner{}, hook.WithoutStopChecks(), hook.WithoutAnnouncement(),
		hook.WithGoals(func(context.Context, string) (string, string, bool, error) {
			return "goal: 1 of 2 done", "", false, nil
		}))

	start := func(source string) string {
		res, err := fire(t, handler, map[string]any{
			"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart", "source": source,
		})
		require.NoError(t, err)

		return res.Specific.AdditionalContext
	}

	assert.Empty(t, start("startup"), "a session that starts has nothing to be reminded of")

	_, err := fire(t, handler, edit(root, "Edit", "a.go"))
	require.NoError(t, err)
	write(t, root, "a.go", "package a\n\nfunc A() {}\n")

	assert.Equal(t, "wavez kept this across the compaction:\nFiles this session changed: a.go.\n"+
		"They have not been checked as they are now.\nGoal: goal: 1 of 2 done", start("compact"))

	id, err := session.Fingerprint(root, []string{"a.go"})
	require.NoError(t, err)

	_, err = session.NewStore(root).Update(t.Context(), sessionID, func(s *session.State) error {
		s.Pass(id)

		return nil
	})
	require.NoError(t, err)

	assert.Contains(t, start("compact"), "The checks passed on them as they are now.")
	assert.Empty(t, start("resume"))
}

func TestHandle_UserPromptSubmit_StartsABuildOfTheBriefedGoPackages(t *testing.T) {
	t.Parallel()

	root := repo(t)
	write(t, root, "pkg/valid/valid.go", "package valid\n\nfunc passwordRule(pw string) bool { return pw != \"\" }\n")
	write(t, root, "web/label.ts", "export function labelFor(id: string): string { return id }\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "more")

	var built [][]string

	handler := hook.New(&fakeRunner{}, hook.WithBrief(), hook.WithoutStopChecks(),
		hook.WithBuildAhead(func(_, id string, pkgs []string) error {
			built = append(built, append([]string{id}, pkgs...))

			return nil
		}))

	prompt := func(text string) {
		_, err := fire(t, handler, map[string]any{
			"session_id": sessionID, "cwd": root, "hook_event_name": "UserPromptSubmit", "prompt": text,
		})
		require.NoError(t, err)
	}

	prompt("labelFor returns the id unchanged")
	assert.Empty(t, built, "a prompt about no Go code starts no build")

	prompt("passwordRule accepts a blank password")
	assert.Equal(t, [][]string{{sessionID, "./pkg/valid/"}}, built)

	// Neither prompt's name has a test, so neither brief was complete.
	state, ok := session.NewStore(root).Read(sessionID)
	require.True(t, ok)
	assert.Equal(t, 2, state.Prompts)
	assert.Zero(t, state.Briefed)

	write(t, root, "pkg/valid/valid_test.go", "package valid\n\nfunc TestRule(t *testing.T) { passwordRule(\"\") }\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "a test")
	prompt("passwordRule accepts a blank password")

	state, _ = session.NewStore(root).Read(sessionID)
	assert.Equal(t, 3, state.Prompts)
	assert.Equal(t, 1, state.Briefed)
}
