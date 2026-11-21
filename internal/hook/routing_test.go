package hook_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/route"
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
			name: "a rule answers and the model is not asked", text: "In a.go return early when n is 0.",
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
			name: "the hint is off", text: "In a.go return early when n is 0.",
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

	in := bytes.NewBufferString(`{"session_id":"s","cwd":"` + t.TempDir() +
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

	for _, ev := range []map[string]any{
		prompt(root, "In go.mod bump the go line."),
		prompt(root, "thanks"),
		shell(root, "tally", `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`),
		shell(root, "tally", `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`),
		shell(root, "tally", `ls`),
		stop(root, false),
	} {
		_, err := fire(t, handler, ev)
		require.NoError(t, err)
	}

	trace.WriteString("not json\n")

	tally, err := hook.ReadTally(&trace)
	require.NoError(t, err)
	assert.Equal(t, hook.Tally{RouteHints: 1, RenameDenials: 1}, tally)

	_, err = hook.ReadTally(failingReader{})
	require.ErrorIs(t, err, errRead)
}

var errRead = errors.New("disk gone")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errRead }
