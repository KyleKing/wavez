package decide_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
)

var errRead = errors.New("disk gone")

func TestReadCorpus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		wantErr string
		want    []decide.Case
	}{
		{name: "empty", in: ""},
		{
			name: "lines, blank lines, and no trailing newline",
			in:   "{\"state\":\"fix the typo\",\"want\":\"direct\"}\n\n  \n{\"state\":\"where?\",\"want\":\"explore\"}",
			want: []decide.Case{{State: "fix the typo", Want: "direct"}, {State: "where?", Want: "explore"}},
		},
		{
			name: "a decision log line with a label added",
			in: `{"time":"2026-11-21T09:30:00Z","question":"route","choice":"direct","state":"s",` +
				`"want":"split"}` + "\n",
			want: []decide.Case{{State: "s", Want: "split"}},
		},
		{name: "a line that is not JSON", in: `{"state":"a","want":"direct"}` + "\nnope\n", wantErr: "corpus line 2"},
		{name: "a line with no label", in: "{\"state\":\"a\"}\n", wantErr: "corpus line 1: want is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decide.ReadCorpus(strings.NewReader(tt.in))

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadCorpus_ReportsAReadFailure(t *testing.T) {
	t.Parallel()

	_, err := decide.ReadCorpus(iotest.ErrReader(errRead))

	require.ErrorIs(t, err, errRead)
	assert.ErrorContains(t, err, "reading corpus line 1")
}

// corpusBackend is a model that calls everything a question and is out on
// one state, with a rule for typos beside it.
func corpusBackend() (*fakeBackend, []decide.Rule) {
	backend := &fakeBackend{name: "fake", model: "tiny", answer: func(state string) string {
		if state == "outage" {
			return "not a key"
		}

		return "explore"
	}}

	rule := func(state string) (string, bool) {
		return "direct", strings.Contains(state, "typo")
	}

	return backend, []decide.Rule{rule}
}

func TestEvaluate_CountsWhoAnsweredAndWhoWasRight(t *testing.T) {
	t.Parallel()

	backend, rules := corpusBackend()
	d := decide.New(decide.WithBackend(backend), decide.WithCache(t.TempDir()))

	long := "split   this\n" + strings.Repeat("across the whole module ", 10)
	cases := []decide.Case{
		{State: "fix the typo", Want: "direct"},
		{State: "explain the typo handling", Want: "explore"},
		{State: "where is the retry loop?", Want: "explore"},
		{State: "where is the retry loop?", Want: "explore"},
		{State: long, Want: "split"},
		{State: "outage", Want: "direct"},
		{State: "outage", Want: "explore"},
		{State: "mislabeled", Want: "no-such-key"},
	}

	report := decide.Evaluate(t.Context(), d, question(), rules, cases)

	assert.Equal(t, "route", report.Question)
	assert.Equal(t, 8, report.Total)
	assert.Equal(t, 4, report.Correct)
	assert.Zero(t, report.Skipped)
	assert.InDelta(t, 0.5, report.Accuracy(), 1e-9)

	assert.Equal(t, []decide.BackendCount{
		{Backend: decide.BackendRule, Answered: 2, Correct: 1},
		{Backend: decide.BackendCache, Answered: 1, Correct: 1},
		{Backend: "fake", Answered: 3, Correct: 1},
		{Backend: decide.BackendFallback, Answered: 2, Correct: 1},
	}, report.Backends)

	require.Len(t, report.Wrong, 4)
	assert.Equal(t, decide.Miss{
		Want: "explore", Got: "direct", Backend: decide.BackendRule, State: "explain the typo handling",
	}, report.Wrong[0])
	assert.Equal(t, "fake", report.Wrong[1].Backend)
	assert.Equal(t, "split", report.Wrong[1].Want)
	assert.Contains(t, report.Wrong[1].State, "split this across the whole module across", "whitespace is collapsed")
	assert.True(t, strings.HasSuffix(report.Wrong[1].State, "..."))
	assert.LessOrEqual(t, len(report.Wrong[1].State), 83)
	assert.Equal(t, decide.BackendFallback, report.Wrong[2].Backend)
	assert.Equal(t, "no-such-key", report.Wrong[3].Want)
}

func TestEvaluate_StopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	rule := func(string) (string, bool) {
		calls++
		cancel()

		return "direct", true
	}

	cases := []decide.Case{{State: "a", Want: "direct"}, {State: "b", Want: "direct"}, {State: "c", Want: "direct"}}

	report := decide.Evaluate(ctx, decide.New(), question(), []decide.Rule{rule}, cases)

	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, report.Total)
	assert.Equal(t, 2, report.Skipped)
	assert.Contains(t, report.Render(), "skipped: 2 (stopped early)\n")
}

func TestReport_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   string
		report decide.Report
	}{
		{
			name:   "an empty corpus",
			report: decide.Report{Question: "route"},
			want:   "question: route\ncases: 0  correct: 0  accuracy: -\n",
		},
		{
			name: "all right",
			report: decide.Report{
				Question: "route", Total: 2, Correct: 2,
				Backends: []decide.BackendCount{{Backend: decide.BackendRule, Answered: 2, Correct: 2}},
			},
			want: "question: route\ncases: 2  correct: 2  accuracy: 100.0%\n\n" +
				"backend  answered  correct  accuracy\n" +
				"rule     2         2        100.0%\n",
		},
		{
			name: "some wrong",
			report: decide.Report{
				Question: "route", Total: 3, Correct: 2,
				Backends: []decide.BackendCount{
					{Backend: decide.BackendRule, Answered: 1, Correct: 1},
					{Backend: "openai", Answered: 2, Correct: 1},
				},
				Wrong: []decide.Miss{{Want: "split", Got: "explore", Backend: "openai", State: "split this"}},
			},
			want: "question: route\ncases: 3  correct: 2  accuracy: 66.7%\n\n" +
				"backend  answered  correct  accuracy\n" +
				"rule     1         1        100.0%\n" +
				"openai   2         1        50.0%\n" +
				"\nwrong:\n" +
				"want   got      backend  state\n" +
				"split  explore  openai   split this\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.report.Render())
		})
	}
}
