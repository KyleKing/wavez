package route_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestState_ListsABoundedNumberOfPaths(t *testing.T) {
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

func TestState_ALongPromptKeepsItsVerifiedPaths(t *testing.T) {
	t.Parallel()

	long := "internal/deeply/nested/package/with/a/long/name/" + strings.Repeat("y", 150) + ".go"
	root := project(t, "a.go", "internal/b.go", long, "last.go")

	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "paths named before a pasted log",
			prompt: "Fix a.go and internal/b.go, here is the log:\n" + strings.Repeat("panic: é at line 12\n", 2000),
			want:   "existing-paths: a.go internal/b.go",
		},
		{
			name:   "a path named after the part that is cut",
			prompt: strings.Repeat("the build is red and nobody knows why. ", 300) + "Start in last.go",
			want:   "existing-paths: last.go",
		},
		{
			name:   "a very long path beside short ones",
			prompt: "Compare a.go with " + long + " and last.go. " + strings.Repeat("More. ", 2000),
			want:   "existing-paths: a.go " + long + " last.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := route.State(root, tt.prompt)

			assert.LessOrEqual(t, len(state), decide.StateLimit, "the model is sent all of it, so nothing after is cut")
			assert.True(t, strings.HasSuffix(state, "\n[prompt cut]\n\n"+tt.want), state[len(state)-200:])
			assert.True(t, strings.HasPrefix(state, tt.prompt[:1000]), "what is cut is the end of the prompt")
			assert.True(t, utf8.ValidString(state), "the cut is on a character")
		})
	}

	t.Run("more paths than fit are left out whole", func(t *testing.T) {
		t.Parallel()

		names := make([]string, 0, 8)

		for i := range 8 {
			names = append(names, strings.Repeat("d", 99)+string(rune('a'+i))+".go")
		}

		state := route.State(project(t, names...), strings.Join(names, " "))

		_, listed, found := strings.Cut(state, "existing-paths: ")
		require.True(t, found)
		assert.LessOrEqual(t, len(state), decide.StateLimit)
		assert.Equal(t, names[:4], strings.Fields(listed))
	})
}

// repoPaths is what the prompts below name, as the repository under review
// has them.
func repoPaths(t *testing.T) string {
	t.Helper()

	return project(t,
		"README.md", "go.mod", "a.go", ".github/workflows/ci.yml",
		"internal/decide/cache.go", "internal/bench/runner.go", "internal/bench/stats.go",
		"internal/route/route_test.go", "internal/gate/cache.go", "internal/store/cache.go",
		"docs/install.md", "internal/slug/slug.go", "web/src/App.tsx", "cmd/tool/main.go",
	)
}

func TestRules_PassOnWhatTheyCannotCheck(t *testing.T) {
	t.Parallel()

	root := repoPaths(t)

	tests := []struct {
		name   string
		prompt string
	}{
		// A prompt that names an existing file says nothing about where the work is.
		{"a file named as off limits", "Don't touch README.md, but update the install docs"},
		{"a file named as a guess", "I think internal/decide/cache.go has a race but I'm not sure, take a look"},
		{"a bug to find in a named file", "Fix the bug in internal/bench/runner.go"},
		{"failing tests in a named file", "The tests in internal/route/route_test.go are failing, fix them"},
		{"a file named as an example", "Like in internal/gate/cache.go, add an LRU to the session store"},
		{"a file named as a symptom", "The CI job in .github/workflows/ci.yml is red"},
		{"a named file and a small change", "In a.go return early when n is 0."},
		{"a named file and a one-line edit", "Bump the go line in go.mod."},

		// Renames that are not one rename of one name in one place.
		{"a rename across the project", "rename the project from wavez to ripple, in go.mod and in the docs"},
		{"a rename in a file that does not exist", "Rename Read in internal/gone/stats.go to ReadLog."},
		{"a rename to the same name", "Rename Read in internal/bench/stats.go to Read."},
		{"a rename with more work after it", "Rename Read in a.go to ReadLog and then fix the flaky test."},
		{"a rename that asks a question", "Rename Read in internal/bench/stats.go to ReadLog? Or is Load better?"},
		{"a rename with a second sentence", "Rename Read in a.go to ReadLog. Also tidy the package."},
		{"two renames", "Rename Read in a.go to ReadLog and Write in a.go to WriteLog."},
		{"a rename of a file", "Rename internal/bench/stats.go to internal/bench/log.go."},

		// Questions that open like a search for a place and are not one.
		{"an opinion about where", "which package would you put this in"},
		{"a name to invent", "locate me a good name for this function"},
		{"a verb with no object", "Track down"},
		{"a guess asked for", "Where do you think this fails? just guess, don't look"},
		{"a place asked about with an instruction after", "Where is the limiter set? Don't look, just guess."},
		{"where we are", "Where are we on the release?"},
		{"where a pronoun points", "Where does that leave us?"},
		{"where to put something", "Where should the new handler go?"},
		{"a search that names its file", "Where is the limiter set in internal/bench/runner.go?"},
		{"a search and a list", "Find where the limiter is set:\n- then raise it\n- then test it"},

		// Lists that code cannot see are independent.
		{"steps called separate", "Do these separately please:\n- run the tests\n- if they pass, commit\n- push"},
		{
			"independent items that name no paths",
			"Please handle these in parallel:\n* write a changelog entry\n* add a /healthz endpoint\n* update the docs",
		},
		{
			"items in one file",
			"These are independent:\n- add a badge to README.md\n- fix the title in README.md\n- fix README.md",
		},
		{
			"one item inside another's directory",
			"Unrelated cleanups:\n1. tidy internal/bench\n2. fix internal/bench/runner.go\n3. fix docs/install.md",
		},
		{
			"an item that names no path",
			"Unrelated cleanups:\n1. fix docs/install.md\n2. fix README.md\n3. make startup faster",
		},
		{
			"paths per item and no word that they are independent",
			"Do these:\n1. define the interface in a.go\n2. implement it in docs/install.md\n3. test it in go.mod",
		},
		{
			"independent in the lead-in and steps in the items",
			"Unrelated to before:\n1. change a.go\n2. then update internal/slug/slug.go\n3. finally fix go.mod",
		},
		{"two independent items", "These are independent:\n- fix docs/install.md\n- fix README.md"},
		{
			"an inline list",
			"Do each independently: (1) fix docs/install.md, (2) fix README.md, (3) fix a.go.",
		},

		{"conversation", "thanks, that looks right"},
		{"nothing", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := route.Decide(t.Context(), decide.New(), root, tt.prompt)

			assert.Equal(t, decide.BackendFallback, got.Backend, "a rule answered %s", got.Choice)
			assert.Equal(t, route.Silent, got.Choice)
		})
	}
}

func TestRules_AnswerWhatTheyChecked(t *testing.T) {
	t.Parallel()

	root := repoPaths(t)

	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{"a rename in a file", "Rename Read in internal/bench/stats.go to ReadLog.", route.Direct},
		{"a rename in a directory", "Rename the exported function Read in internal/bench to ReadLog", route.Direct},
		{"a rename written file:Symbol", "Rename internal/store/cache.go:evictOldest to evictLRU.", route.Direct},
		{
			"a rename that names its callers",
			"Rename the unexported function Read in `internal/bench/stats.go` to `ReadLog` " +
				"and update every caller in the bench package.",
			route.Direct,
		},
		{
			"a rename with callers, tests, and comments",
			"rename the method Stats.Read in ./internal/bench/ to Stats.ReadLog and update all references " +
				"in the repository, tests and comments included.",
			route.Direct,
		},
		{"where something is", "Where is the rate limiter configured?", route.Explore},
		{"which file does something", "Which file decides whether a request is authenticated?", route.Explore},
		{
			"find where, and say what happens there",
			"Find where we parse the X-Request-Id header and tell me what happens when it is missing.",
			route.Explore,
		},
		{"locate the code", "Locate the code that builds the SQL for the search page.", route.Explore},
		{
			"independent items in files of their own",
			"These are independent, do them separately:\n- add a LICENSE badge to README.md\n" +
				"- bump the Go version in .github/workflows/ci.yml to 1.26\n" +
				"- delete the dead flag --legacy from cmd/tool/main.go",
			route.Coordinate,
		},
		{
			"numbered items, one with a second line",
			"Three unrelated cleanups:\n1. Fix the broken link in docs/install.md.\n" +
				"2. Add a unit test for internal/slug/slug.go:Make.\n   Keep it table-driven.\n" +
				"3. Remove the commented-out block at the top of web/src/App.tsx.",
			route.Coordinate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := route.Decide(t.Context(), decide.New(), root, tt.prompt)

			assert.Equal(t, decide.BackendRule, got.Backend)
			assert.Equal(t, tt.want, got.Choice)
		})
	}
}

func TestRules_PassOnAPromptThatWasCut(t *testing.T) {
	t.Parallel()

	root := repoPaths(t)
	prompt := "Rename Read in internal/bench/stats.go to ReadLog" + strings.Repeat(" ", 2000) + "and then rewrite it."

	got := route.Decide(t.Context(), decide.New(), root, prompt)

	assert.Equal(t, decide.BackendFallback, got.Backend, "the part that was cut could say anything")
}

func TestDecide(t *testing.T) {
	t.Parallel()

	root := project(t, "a.go")

	got := route.Decide(t.Context(), decide.New(), root, "Rename Read in a.go to ReadLog.")
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

func corpus(t *testing.T, name string) []decide.Case {
	t.Helper()

	file, err := os.Open(filepath.Join("..", "..", "bench", name)) //nolint:gosec // a corpus of this repository
	require.NoError(t, err)

	t.Cleanup(func() { assert.NoError(t, file.Close()) })

	cases, err := decide.ReadCorpus(file)
	require.NoError(t, err)

	return cases
}

// TestRules_AreNeverWrongOnTheSeedCorpus is a regression check and no
// measurement: the rules were written with this corpus in view. A rule may
// pass on a line, and may not answer one wrongly.
func TestRules_AreNeverWrongOnTheSeedCorpus(t *testing.T) {
	t.Parallel()

	cases := corpus(t, "route_corpus.jsonl")
	require.GreaterOrEqual(t, len(cases), 40)

	report := decide.Evaluate(t.Context(), decide.New(), route.Question(), route.Rules(), cases, 1)

	for _, miss := range report.Wrong {
		assert.NotEqual(t, decide.BackendRule, miss.Backend, "a rule answered %s for %q, labeled %s",
			miss.Got, miss.State, miss.Want)
	}

	assert.Positive(t, report.Rules.Answered)
}

// TestRules_OnTheHeldOutCorpus runs the rules over prompts labeled by
// someone who had not seen them. It holds two things: the file stays a
// corpus the route question can be scored on, and no rule answers a line of
// it wrongly. A failure of the second is a finding about the rule to
// report, and never a reason to fit the rule to the line, which would make
// this corpus one more the rules were tuned on.
func TestRules_OnTheHeldOutCorpus(t *testing.T) {
	t.Parallel()

	cases := corpus(t, "route_holdout.jsonl")
	require.GreaterOrEqual(t, len(cases), 80)

	q := route.Question()

	for _, c := range cases {
		assert.True(t, c.Want == q.Fallback || route.Hint(c.Want) != "", "%q is not a route: %s", c.Want, c.State)
	}

	report := decide.Evaluate(t.Context(), decide.New(), q, route.Rules(), cases, 1)

	t.Logf("rules answered %d of %d, %d right", report.Rules.Answered, report.Total, report.Rules.Correct)

	for _, miss := range report.Wrong {
		assert.NotEqual(t, decide.BackendRule, miss.Backend, "a rule answered %s for %q, labeled %s",
			miss.Got, miss.State, miss.Want)
	}
}
