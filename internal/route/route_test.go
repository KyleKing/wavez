package route_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/route"
)

// answering is a backend that always gives one key.
type answering string

func (answering) Name() string { return "fake" }

func (a answering) Answer(context.Context, decide.Question, string) (decide.Answer, error) {
	return decide.Answer{Key: string(a)}, nil
}

func project(t *testing.T, paths ...string) string {
	t.Helper()

	root := t.TempDir()

	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte("x\n"), 0o600))
	}

	return root
}

func TestQuestion(t *testing.T) {
	t.Parallel()

	q := route.Question()

	keys := make([]string, 0, len(q.Choices))
	for _, choice := range q.Choices {
		keys = append(keys, choice.Key)
		assert.NotEmpty(t, choice.Description)
	}

	assert.Equal(t, []string{route.Direct, route.Explore, route.Coordinate, route.Silent}, keys)
	assert.Equal(t, route.Silent, q.Fallback, "the fallback is to say nothing")
	assert.Equal(t, route.QuestionName, q.Name)
}

func TestHint(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, key := range []string{route.Direct, route.Explore, route.Coordinate} {
		hint := route.Hint(key)
		require.NotEmpty(t, hint, key)
		assert.True(t, strings.HasPrefix(hint, "wavez: "), hint)
		assert.NotContains(t, hint, "\n", "a hint is one sentence")
		assert.False(t, seen[hint], "each route has its own hint")

		seen[hint] = true
	}

	assert.Empty(t, route.Hint(route.Silent), "the fallback says nothing")
	assert.Empty(t, route.Hint("made up by a model"))
}

func TestState(t *testing.T) {
	t.Parallel()

	root := project(t, "internal/bench/stats.go", "README.md", "docs/guide.md", "cmd/tool/main.go")

	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{"no path named", "  Where is the limiter?  ", "Where is the limiter?"},
		{
			"a file, a directory, and a path that does not exist",
			"Rename Read in internal/bench/stats.go, used by internal/bench and internal/gone/x.go.",
			"Rename Read in internal/bench/stats.go, used by internal/bench and internal/gone/x.go." +
				"\n\nexisting-paths: internal/bench/stats.go internal/bench/",
		},
		{
			"punctuation, a symbol suffix, a possessive, and a repeat",
			"Fix `cmd/tool/main.go:Run` (see README.md's intro, and ./README.md).",
			"Fix `cmd/tool/main.go:Run` (see README.md's intro, and ./README.md)." +
				"\n\nexisting-paths: cmd/tool/main.go README.md",
		},
		{
			"a URL, an absolute path, and a path that climbs out are not looked up",
			"See https://example.com/docs/guide.md and /etc/passwd and ../docs/guide.md, e.g. v1.2.",
			"See https://example.com/docs/guide.md and /etc/passwd and ../docs/guide.md, e.g. v1.2.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, route.State(root, tt.prompt))
		})
	}
}

func TestState_IsBounded(t *testing.T) {
	t.Parallel()

	const named = 40

	var prompt strings.Builder

	paths := make([]string, 0, named)

	for i := range named {
		path := "f" + strings.Repeat("x", i) + ".go"
		paths = append(paths, path)
		prompt.WriteString(path + " ")
	}

	state := route.State(project(t, paths...), prompt.String())
	_, listed, found := strings.Cut(state, "existing-paths: ")

	require.True(t, found)
	assert.Len(t, strings.Fields(listed), 8, "a pasted listing does not become a long state")
}

func TestRules(t *testing.T) {
	t.Parallel()

	const file = "\n\nexisting-paths: internal/x/a.go"

	tests := []struct {
		name  string
		state string
		want  string
	}{
		{"a named file and a short instruction", "In internal/x/a.go return early when n is 0." + file, route.Direct},
		{"a rename under a directory", "Rename Read in internal/x to ReadLog and update every caller." +
			"\n\nexisting-paths: internal/x/", route.Direct},
		{"a where question", "Where is the rate limiter configured?", route.Explore},
		{"a which-file question", "Which file holds the retry policy?", route.Explore},
		{"a list the prompt calls independent", "Do these independently:\n- a\n- b\n- c", route.Coordinate},
		{"inline numbers called unrelated", "Unrelated: (1) fix a, (2) fix b, (3) fix c.", route.Coordinate},

		{"a path that does not exist", "In internal/x/a.go return early when n is 0.", ""},
		{"a directory alone is not a place to edit", "Tidy internal/x.\n\nexisting-paths: internal/x/", ""},
		{"a question about a named file", "Is internal/x/a.go safe to delete?" + file, ""},
		{"an investigation that names a file", "Figure out why internal/x/a.go leaks." + file, ""},
		{"a change that sweeps past the file", "Apply the fix in internal/x/a.go to every other package." + file, ""},
		{"a long prompt", "In internal/x/a.go " + strings.Repeat("do more ", 60) + file, ""},
		{"a long rename under a directory", "Rename a in internal/x " + strings.Repeat("and more ", 30) +
			"\n\nexisting-paths: internal/x/", ""},
		{"a list of steps in a named file", "In internal/x/a.go:\n1. add f\n2. then call it\n3. test it" + file, ""},
		{"a list of steps called separate", "Separate commits:\n1. add f\n2. then call it\n3. finally test", ""},
		{"a list nobody called independent", "Todo:\n- a\n- b\n- c", ""},
		{"two independent items are not a plan", "Independently:\n- a\n- b", ""},
		{"a where question that names the place", "Where in internal/x/a.go is the limiter set" + file, ""},
		{"conversation", "thanks, go ahead", ""},
		{"a header that is not the last line", "x\nexisting-paths: a.go\nIn a.go fix it", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := decide.New().Decide(t.Context(), route.Question(), tt.state, route.Rules()...)

			if tt.want == "" {
				assert.Equal(t, decide.BackendFallback, got.Backend, "no rule should fire, got %s", got.Choice)
				assert.Equal(t, route.Silent, got.Choice)

				return
			}

			assert.Equal(t, decide.BackendRule, got.Backend)
			assert.Equal(t, tt.want, got.Choice)
		})
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()

	root := project(t, "a.go")

	got := route.Decide(t.Context(), decide.New(), root, "In a.go return early.")
	assert.Equal(t, route.Direct, got.Choice)
	assert.Equal(t, decide.BackendRule, got.Backend)

	got = route.Decide(t.Context(), decide.New(), root, "Make it faster.")
	assert.Equal(t, route.Silent, got.Choice, "no rule and no model is the fallback")

	got = route.Decide(t.Context(), decide.New(decide.WithBackend(answering(route.Explore))), root, "Make it faster.")
	assert.Equal(t, route.Explore, got.Choice)
	assert.Equal(t, "fake", got.Backend)

	got = route.Decide(t.Context(), decide.New(decide.WithBackend(answering("anything else"))), root, "Make it faster.")
	assert.Equal(t, route.Silent, got.Choice, "an answer outside the set is the fallback")
}

// TestRules_AreNeverWrongOnTheCorpus holds the rules to the seed corpus: a
// rule may pass on a line, and may not answer one wrongly.
func TestRules_AreNeverWrongOnTheCorpus(t *testing.T) {
	t.Parallel()

	file, err := os.Open(filepath.Join("..", "..", "bench", "route_corpus.jsonl"))
	require.NoError(t, err)

	t.Cleanup(func() { assert.NoError(t, file.Close()) })

	cases, err := decide.ReadCorpus(file)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cases), 40)

	report := decide.Evaluate(t.Context(), decide.New(), route.Question(), route.Rules(), cases)

	for _, miss := range report.Wrong {
		assert.NotEqual(t, decide.BackendRule, miss.Backend, "a rule answered %s for %q, labeled %s",
			miss.Got, miss.State, miss.Want)
	}

	for _, count := range report.Backends {
		if count.Backend == decide.BackendRule {
			assert.GreaterOrEqual(t, count.Answered, len(cases)/3, "the rules should still answer a useful share")
		}
	}
}
