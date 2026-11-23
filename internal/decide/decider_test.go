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
	"sync"
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

// fakeBackend answers with a fixed key or error.
type fakeBackend struct {
	err    error
	answer func(state string) string
	name   string
	model  string
	panics bool
}

func (f *fakeBackend) Name() string { return f.name }

func (f *fakeBackend) Answer(_ context.Context, _ decide.Question, state string) (decide.Answer, error) {
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

	assert.Equal(t, "tiny", got.Model)
	assert.Equal(t, 40, got.InputTokens)
	assert.Equal(t, 1, got.OutputTokens)
}

func TestDecide_TheSameStateIsAskedEveryTime(t *testing.T) {
	t.Parallel()

	asked := 0
	backend := &fakeBackend{name: "fake", answer: func(string) string {
		asked++

		return "explore"
	}}
	d := decide.New(decide.WithBackend(backend))

	d.Decide(t.Context(), question(), "state")
	d.Decide(t.Context(), question(), "state")

	assert.Equal(t, 2, asked, "nothing is cached, so nothing is left on disk per prompt")
}

// logLine is the shape of one line of the decision log.
//
//nolint:govet // fieldalignment: the fields are in the order the log writes them
type logLine struct {
	Time time.Time `json:"time"`
	decide.Decision
	StateSHA256 string `json:"state_sha256"`
	State       string `json:"state"`
}

func readLog(t *testing.T, path string) []logLine {
	t.Helper()

	f, err := os.Open(path) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)

	defer f.Close() //nolint:errcheck // read-only

	var lines []logLine

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line logLine

		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line), "every line is one whole record")

		lines = append(lines, line)
	}

	require.NoError(t, scanner.Err())

	return lines
}

func TestLog_EveryDecisionIsOneLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".wavez", "decisions.jsonl")
	backend := &fakeBackend{name: "fake", model: "tiny", answer: always("explore")}
	d := decide.New(decide.WithBackend(backend), decide.WithLog(path))

	long := strings.Repeat("é", 400)
	sum := sha256.Sum256([]byte(long))
	before := time.Now().Add(-time.Second)

	d.Decide(t.Context(), question(), "short", func(string) (string, bool) { return "split", true })
	d.Decide(t.Context(), question(), long)

	backend.err = errBackend
	d.Decide(t.Context(), question(), "other")

	lines := readLog(t, path)
	require.Len(t, lines, 3)

	backends := make([]string, 0, len(lines))
	for _, line := range lines {
		backends = append(backends, line.Backend)

		assert.WithinRange(t, line.Time, before, time.Now().Add(time.Second))
	}

	assert.Equal(t, []string{decide.BackendRule, "fake", decide.BackendFallback}, backends)
	assert.Equal(t, "short", lines[0].State)
	assert.Equal(t, "backend down", lines[2].Reason)

	assert.Equal(t, hex.EncodeToString(sum[:]), lines[1].StateSHA256, "the hash is of the whole state")
	assert.Equal(t, strings.Repeat("é", 250), lines[1].State, "the excerpt is cut on a character")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a prompt excerpt is the user's alone to read")
}

func TestLog_LineKeysAreStable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "decisions.jsonl")

	decide.New(decide.WithLog(path)).Decide(t.Context(), question(), "fix the typo")

	data, err := os.ReadFile(path) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)

	sum := sha256.Sum256([]byte("fix the typo"))

	assert.Regexp(t,
		`^\{"time":"[^"]+Z","question":"route","choice":"direct","backend":"fallback",`+
			`"reason":"no backend","latency_ns":\d+,"state_sha256":"`+hex.EncodeToString(sum[:])+
			`","state":"fix the typo"\}\n$`,
		string(data))
}

func TestLog_RotatesPastAMegabyte(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	old := strings.Repeat(`{"state":"an old decision"}`+"\n", 1<<20/28+1)
	require.NoError(t, os.WriteFile(path, []byte(old), 0o600))

	d := decide.New(decide.WithLog(path))

	d.Decide(t.Context(), question(), "first after the cap")
	d.Decide(t.Context(), question(), "second after the cap")

	lines := readLog(t, path)
	require.Len(t, lines, 2, "the log starts again once it passes the cap")
	assert.Equal(t, "first after the cap", lines[0].State)

	rotated, err := os.ReadFile(path + ".1") //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, old, string(rotated), "the newest megabyte before the cap is kept beside it")

	// A second rotation replaces the first, so the two files are all there is.
	require.NoError(t, os.WriteFile(path, []byte(old), 0o600))
	d.Decide(t.Context(), question(), "after the second cap")

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestLog_ConcurrentAppendsNeverInterleave(t *testing.T) {
	t.Parallel()

	const writers, each = 16, 25

	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	state := strings.Repeat("a long state that fills a line ", 40)

	var wg sync.WaitGroup

	for range writers {
		// Each writer is its own Engine, as each hook process is.
		wg.Go(func() {
			d := decide.New(decide.WithLog(path))
			for range each {
				d.Decide(t.Context(), question(), state)
			}
		})
	}

	wg.Wait()

	assert.Len(t, readLog(t, path), writers*each)
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
