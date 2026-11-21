package decide_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
)

var errBackend = errors.New("backend down")

func question() decide.Question {
	return decide.Question{
		Name:         "route",
		Instructions: "What kind of work is this prompt?",
		Fallback:     "direct",
		Choices: []decide.Choice{
			{Key: "direct", Description: "A change to make now."},
			{Key: "explore", Description: "A question about the code."},
			{Key: "split", Description: "Work to divide between workers."},
		},
	}
}

// fakeBackend answers with a fixed key or error and counts its calls.
type fakeBackend struct {
	err    error
	answer func(state string) string
	name   string
	model  string
	calls  int
	panics bool
}

func (f *fakeBackend) Name() string { return f.name }

func (f *fakeBackend) Model() string { return f.model }

func (f *fakeBackend) Answer(_ context.Context, _ decide.Question, state string) (decide.Answer, error) {
	f.calls++

	if f.panics {
		panic("backend bug")
	}

	if f.err != nil {
		return decide.Answer{}, f.err
	}

	return decide.Answer{Key: f.answer(state), Model: f.model, InputTokens: 40, OutputTokens: 1}, nil
}

func always(key string) func(string) string {
	return func(string) string { return key }
}

// bareBackend is a Backend that does not say which model it asks.
type bareBackend struct{ calls int }

func (*bareBackend) Name() string { return "bare" }

func (b *bareBackend) Answer(context.Context, decide.Question, string) (decide.Answer, error) {
	b.calls++

	return decide.Answer{Key: "split"}, nil
}

func pass(string) (string, bool) { return "", false }

func TestDecide_OrderIsRulesThenBackendThenFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		backend     decide.Backend
		name        string
		wantChoice  string
		wantBackend string
		wantReason  string
		rules       []decide.Rule
	}{
		{
			name:       "a rule that is sure answers before the model",
			backend:    &fakeBackend{name: "fake", answer: always("split")},
			rules:      []decide.Rule{pass, func(string) (string, bool) { return "explore", true }},
			wantChoice: "explore", wantBackend: decide.BackendRule,
		},
		{
			name:       "a rule naming a key outside the set has passed",
			backend:    &fakeBackend{name: "fake", answer: always("split")},
			rules:      []decide.Rule{func(string) (string, bool) { return "delete-everything", true }},
			wantChoice: "split", wantBackend: "fake",
		},
		{
			name:       "a rule that panics has passed",
			backend:    &fakeBackend{name: "fake", answer: always("split")},
			rules:      []decide.Rule{func(string) (string, bool) { panic("rule bug") }},
			wantChoice: "split", wantBackend: "fake",
		},
		{
			name:       "no rule and no backend is the fallback",
			wantChoice: "direct", wantBackend: decide.BackendFallback, wantReason: "no backend",
		},
		{
			name:       "a backend error is the fallback",
			backend:    &fakeBackend{name: "fake", err: errBackend},
			wantChoice: "direct", wantBackend: decide.BackendFallback, wantReason: "backend down",
		},
		{
			name:       "an answer outside the set is the fallback",
			backend:    &fakeBackend{name: "fake", answer: always("rm -rf")},
			wantChoice: "direct", wantBackend: decide.BackendFallback, wantReason: "answer outside the set",
		},
		{
			name:       "a backend that panics is the fallback",
			backend:    &fakeBackend{name: "fake", panics: true},
			wantChoice: "direct", wantBackend: decide.BackendFallback, wantReason: "panic: backend bug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []decide.Option
			if tt.backend != nil {
				opts = append(opts, decide.WithBackend(tt.backend))
			}

			got := decide.New(opts...).Decide(t.Context(), question(), "state", tt.rules...)

			assert.Equal(t, "route", got.Question)
			assert.Equal(t, tt.wantChoice, got.Choice)
			assert.Equal(t, tt.wantBackend, got.Backend)
			assert.Equal(t, tt.wantReason, got.Reason)
		})
	}
}

func TestDecide_CarriesTheModelAndItsTokens(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{name: "fake", model: "tiny", answer: always("explore")}

	got := decide.New(decide.WithBackend(backend)).Decide(t.Context(), question(), "state")

	assert.Equal(t, decide.Decision{
		Question: "route", Choice: "explore", Backend: "fake", Model: "tiny",
		InputTokens: 40, OutputTokens: 1, Latency: got.Latency,
	}, got)
}

type panickyName struct{ fakeBackend }

func (*panickyName) Name() string { panic("no name") }

func TestDecide_ABackendThatCannotNameItselfIsTheFallback(t *testing.T) {
	t.Parallel()

	got := decide.New(decide.WithBackend(&panickyName{})).Decide(t.Context(), question(), "state")

	assert.Equal(t, decide.BackendFallback, got.Backend)
	assert.Equal(t, "panic: no name", got.Reason)
}

func TestCache_AModelAnswerIsAskedOnce(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "cache", "decide")
	backend := &fakeBackend{name: "fake", model: "tiny", answer: always("explore")}
	d := decide.New(decide.WithBackend(backend), decide.WithCache(dir))

	first := d.Decide(t.Context(), question(), "state")
	second := d.Decide(t.Context(), question(), "state")

	assert.Equal(t, "fake", first.Backend)
	assert.Equal(t, decide.BackendCache, second.Backend)
	assert.Equal(t, "explore", second.Choice)
	assert.Equal(t, "tiny", second.Model, "a hit names the model that answered")
	assert.Zero(t, second.InputTokens, "a hit spent no tokens")
	assert.Equal(t, 1, backend.calls)
}

func TestCache_KeyCoversEverythingThatCouldChangeTheAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		change func(q *decide.Question, b *fakeBackend, state *string)
		name   string
	}{
		{name: "state", change: func(_ *decide.Question, _ *fakeBackend, s *string) { *s = "another state" }},
		{name: "question name", change: func(q *decide.Question, _ *fakeBackend, _ *string) { q.Name = "other" }},
		{name: "instructions", change: func(q *decide.Question, _ *fakeBackend, _ *string) { q.Instructions = "Why?" }},
		{name: "a description", change: func(q *decide.Question, _ *fakeBackend, _ *string) {
			q.Choices[1].Description = "Reading only."
		}},
		{name: "a new choice", change: func(q *decide.Question, _ *fakeBackend, _ *string) {
			q.Choices = append(q.Choices, decide.Choice{Key: "ask", Description: "Unclear."})
		}},
		{name: "backend name", change: func(_ *decide.Question, b *fakeBackend, _ *string) { b.name = "other" }},
		{name: "model", change: func(_ *decide.Question, b *fakeBackend, _ *string) { b.model = "bigger" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backend := &fakeBackend{name: "fake", model: "tiny", answer: always("explore")}
			d := decide.New(decide.WithBackend(backend), decide.WithCache(t.TempDir()))
			q, state := question(), "state"

			d.Decide(t.Context(), q, state)
			tt.change(&q, backend, &state)
			got := d.Decide(t.Context(), q, state)

			assert.Equal(t, backend.name, got.Backend)
			assert.Equal(t, 2, backend.calls)
		})
	}
}

func TestCache_NeverHoldsARuleOrAFallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	backend := &fakeBackend{name: "fake", err: errBackend}
	d := decide.New(decide.WithBackend(backend), decide.WithCache(dir))

	d.Decide(t.Context(), question(), "state")
	d.Decide(t.Context(), question(), "state", func(string) (string, bool) { return "split", true })

	backend.err, backend.answer = nil, always("not a key")
	d.Decide(t.Context(), question(), "state")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Equal(t, 2, backend.calls, "each failure asks again")
}

func TestCache_AnEntryThatCannotBeUsedIsAMiss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry string
	}{
		{"not JSON", "{half"},
		{"a choice that is no longer in the set", `{"question":"route","choice":"retired","backend":"fake"}`},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			backend := &fakeBackend{name: "fake", answer: always("explore")}
			d := decide.New(decide.WithBackend(backend), decide.WithCache(dir))

			d.Decide(t.Context(), question(), "state")

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.NoError(t, os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte(tt.entry), 0o600))

			got := d.Decide(t.Context(), question(), "state")

			assert.Equal(t, "fake", got.Backend)
			assert.Equal(t, "explore", got.Choice)
			assert.Equal(t, 2, backend.calls)

			again := d.Decide(t.Context(), question(), "state")
			assert.Equal(t, decide.BackendCache, again.Backend, "the miss rewrote the entry")
		})
	}
}

func TestCache_ADirectoryThatCannotBeWrittenChangesNothing(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	backend := &fakeBackend{name: "fake", answer: always("explore")}
	d := decide.New(decide.WithBackend(backend), decide.WithCache(filepath.Join(blocker, "cache")))

	for range 2 {
		got := d.Decide(t.Context(), question(), "state")

		assert.Equal(t, "fake", got.Backend)
		assert.Equal(t, "explore", got.Choice)
	}
}

func TestCache_ABackendWithoutAModelStillCaches(t *testing.T) {
	t.Parallel()

	backend := &bareBackend{}
	d := decide.New(decide.WithBackend(backend), decide.WithCache(t.TempDir()))

	d.Decide(t.Context(), question(), "state")
	got := d.Decide(t.Context(), question(), "state")

	assert.Equal(t, decide.BackendCache, got.Backend)
	assert.Empty(t, got.Model)
	assert.Equal(t, 1, backend.calls)
}

func readLog(t *testing.T, path string) []decide.Record {
	t.Helper()

	f, err := os.Open(path) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)

	defer f.Close() //nolint:errcheck // read-only

	var records []decide.Record

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var r decide.Record

		require.NoError(t, json.Unmarshal(scanner.Bytes(), &r))

		records = append(records, r)
	}

	require.NoError(t, scanner.Err())

	return records
}

func TestLog_EveryDecisionIsOneLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".wavez", "decisions.jsonl")
	at := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	backend := &fakeBackend{name: "fake", model: "tiny", answer: always("explore")}
	d := decide.New(
		decide.WithBackend(backend),
		decide.WithCache(t.TempDir()),
		decide.WithLog(path),
		decide.WithClock(func() time.Time { return at }),
	)

	long := strings.Repeat("é", 400)
	sum := sha256.Sum256([]byte(long))

	d.Decide(t.Context(), question(), "short", func(string) (string, bool) { return "split", true })
	d.Decide(t.Context(), question(), long)
	d.Decide(t.Context(), question(), long)

	backend.err = errBackend
	d.Decide(t.Context(), question(), "other")

	records := readLog(t, path)
	require.Len(t, records, 4)

	backends := make([]string, 0, len(records))
	for _, r := range records {
		backends = append(backends, r.Backend)

		assert.True(t, r.Time.Equal(at))
		assert.Zero(t, r.Latency, "the injected clock measures the latency too")
	}

	assert.Equal(t, []string{decide.BackendRule, "fake", decide.BackendCache, decide.BackendFallback}, backends)
	assert.Equal(t, "short", records[0].State)
	assert.Equal(t, "backend down", records[3].Reason)

	assert.Equal(t, hex.EncodeToString(sum[:]), records[1].StateSHA256, "the hash is of the whole state")
	assert.Equal(t, strings.Repeat("é", 250), records[1].State, "the excerpt is cut on a character")
}

func TestLog_LineKeysAreStable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	at := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)

	decide.New(decide.WithLog(path), decide.WithClock(func() time.Time { return at })).
		Decide(t.Context(), question(), "fix the typo")

	data, err := os.ReadFile(path) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)

	sum := sha256.Sum256([]byte("fix the typo"))

	assert.Equal(t,
		`{"time":"2026-11-21T09:30:00Z","question":"route","choice":"direct","backend":"fallback",`+
			`"reason":"no backend","latency_ns":0,"state_sha256":"`+hex.EncodeToString(sum[:])+
			`","state":"fix the typo"}`+"\n",
		string(data))
}

func TestLog_AWriteFailureNeverChangesTheDecision(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	tests := []struct {
		name string
		path string
	}{
		{"the parent cannot be made", filepath.Join(blocker, "sub", "decisions.jsonl")},
		{"the path is a directory", dir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backend := &fakeBackend{name: "fake", answer: always("split")}

			got := decide.New(decide.WithBackend(backend), decide.WithLog(tt.path)).
				Decide(t.Context(), question(), "state")

			assert.Equal(t, "split", got.Choice)
			assert.Equal(t, "fake", got.Backend)
		})
	}
}
