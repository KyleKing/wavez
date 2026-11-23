package decide_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
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
			in:   `{"state":"a","want":"direct"}` + "\n\n  \n" + `{"state":"b\nc","want":"explore"}`,
			want: []decide.Case{{State: "a", Want: "direct"}, {State: "b\nc", Want: "explore"}},
		},
		{
			name: "a decision log line with a label added, and a note beside it",
			in:   `{"time":"2026-11-21T09:30:00Z","choice":"direct","state":"a","want":"explore","why":"x"}` + "\n",
			want: []decide.Case{{State: "a", Want: "explore"}},
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

func corpusCases() []decide.Case {
	return []decide.Case{
		{State: "fix the typo", Want: "direct"},
		{State: "explain the typo handling", Want: "explore"},
		{State: "where is the retry loop?", Want: "explore"},
		{State: "split   this\n" + strings.Repeat("across the whole module ", 10), Want: "split"},
		{State: "outage", Want: "direct"},
		{State: "outage", Want: "explore"},
		{State: "mislabeled", Want: "no-such-key"},
		{State: "thanks", Want: "direct"},
	}
}

func TestEvaluate_KeepsTheRulesApartFromWhatFollowsThem(t *testing.T) {
	t.Parallel()

	backend, rules := corpusBackend()

	report := decide.Evaluate(t.Context(), decide.New(decide.WithBackend(backend)), question(), rules, corpusCases(), 3)

	assert.Equal(t, "route", report.Question)
	assert.Equal(t, 8, report.Total)
	assert.Zero(t, report.Skipped)

	assert.Equal(t, decide.Tally{Answered: 2, Correct: 1}, report.Rules, "coverage 2 of 8, precision 1 of 2")
	assert.Equal(t, 6, report.Left())
	assert.Equal(t,
		[]decide.ModelTally{{Backend: "fake", Model: "tiny", Tally: decide.Tally{Answered: 4, Correct: 1}}},
		report.Models)
	assert.Equal(t, decide.Tally{Answered: 2, Correct: 1}, report.FellBack)
	assert.Equal(t, 2, report.LeftForFallback, "of the six the rules left, two are labeled with the fallback")
	assert.Equal(t, map[string]int{"answer outside the set": 2}, report.Reasons)

	require.Len(t, report.Wrong, 5)
	assert.Equal(t, decide.Miss{
		Want: "explore", Got: "direct", Backend: decide.BackendRule, State: "explain the typo handling",
	}, report.Wrong[0], "misses stay in corpus order whatever order they finished in")
	assert.Equal(t, "fake", report.Wrong[1].Backend)
	assert.Contains(t, report.Wrong[1].State, "split this across the whole module across", "whitespace is collapsed")
	assert.True(t, strings.HasSuffix(report.Wrong[1].State, "..."))
	assert.Equal(t, decide.BackendFallback, report.Wrong[2].Backend)
	assert.Equal(t, "no-such-key", report.Wrong[3].Want)

	assert.Equal(t, `question: route
cases: 8

rules: answered 2 of 8 (coverage 25.0%); 1 right, 1 wrong (precision 50.0%)

the 6 cases the rules left:
  fake tiny: answered 4; 1 right, 3 wrong (25.0%)
  fallback: 2 got "direct"; 1 right
    2: answer outside the set
  with no model: "direct" for all 6 would be right 2 times (33.3%)

overall: 3 of 8 right (37.5%)

wrong by rule:
want     got     state
explore  direct  explain the typo handling

wrong by fake:
want         got      state
split        explore  `+report.Wrong[1].State+`
no-such-key  explore  mislabeled
direct       explore  thanks

wrong by fallback:
want     got     state
explore  direct  outage
`, report.Render())
}

func TestEvaluate_RulesAloneReportCoverageAndPrecision(t *testing.T) {
	t.Parallel()

	_, rules := corpusBackend()

	report := decide.Evaluate(t.Context(), decide.New(), question(), rules, corpusCases(), 1)

	assert.Equal(t, decide.Tally{Answered: 2, Correct: 1}, report.Rules)
	assert.Empty(t, report.Models)
	assert.Equal(t, decide.Tally{Answered: 6, Correct: 2}, report.FellBack)
	assert.Contains(t, report.Render(),
		"rules: answered 2 of 8 (coverage 25.0%); 1 right, 1 wrong (precision 50.0%)\n\n"+
			"the 6 cases the rules left:\n"+
			"  fallback: 6 got \"direct\"; 2 right\n"+
			"    6: no backend\n")
}

func TestEvaluate_AnEmptyCorpusRenders(t *testing.T) {
	t.Parallel()

	report := decide.Evaluate(t.Context(), decide.New(), question(), nil, nil, 1)

	assert.Contains(t, report.Render(), "rules: answered 0 of 0 (coverage -); 0 right, 0 wrong (precision -)\n")
	assert.Contains(t, report.Render(), "overall: 0 of 0 right (-)\n")
}

// gate is a backend that records how many answers are in flight at once.
type gate struct {
	release chan struct{}
	now     atomic.Int32
	peak    atomic.Int32
	asked   atomic.Int32
}

func (*gate) Name() string { return "gate" }

func (g *gate) Answer(context.Context, decide.Question, string) (decide.Answer, error) {
	g.asked.Add(1)

	now := g.now.Add(1)
	defer g.now.Add(-1)

	for {
		peak := g.peak.Load()
		if now <= peak || g.peak.CompareAndSwap(peak, now) {
			break
		}
	}

	<-g.release

	return decide.Answer{Key: "explore"}, nil
}

func TestEvaluate_AsksNoMoreAtOnceThanItIsTold(t *testing.T) {
	t.Parallel()

	const parallel, total = 4, 20

	backend := &gate{release: make(chan struct{})}
	cases := make([]decide.Case, total)

	for i := range cases {
		cases[i] = decide.Case{State: "state", Want: "explore"}
	}

	var (
		report decide.Report
		done   sync.WaitGroup
	)

	done.Go(func() {
		report = decide.Evaluate(t.Context(), decide.New(decide.WithBackend(backend)), question(), nil, cases, parallel)
	})

	// Let the first batch pile up against the gate before opening it.
	for range parallel {
		backend.release <- struct{}{}
	}

	close(backend.release)
	done.Wait()

	assert.Equal(t, total, report.Total)
	assert.EqualValues(t, total, backend.asked.Load())
	assert.LessOrEqual(t, backend.peak.Load(), int32(parallel))
	assert.Equal(t, total, report.Models[0].Correct)
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

	report := decide.Evaluate(ctx, decide.New(), question(), []decide.Rule{rule}, cases, 1)

	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, report.Total)
	assert.Equal(t, 2, report.Skipped)
	assert.Contains(t, report.Render(), "skipped: 2 (stopped early)\n")
}
