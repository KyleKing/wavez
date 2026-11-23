package report_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/report"
	"github.com/kyleking/wavez/internal/transcript"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixture is the synthetic session the transcript package tests against.
func fixture(t *testing.T, opts ...transcript.TimelineOption) transcript.SessionTimeline {
	t.Helper()

	tl, err := transcript.ReadTimeline(filepath.Join("..", "transcript", "testdata", "timeline.jsonl"), opts...)
	require.NoError(t, err)

	return tl
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}

	want, err := os.ReadFile(path) //nolint:gosec // a fixture under this package's testdata
	require.NoError(t, err, "run `go test ./internal/report -update` to write it")
	assert.Equal(t, string(want), string(got))
}

func TestPage_HTMLMatchesTheGoldenFile(t *testing.T) {
	t.Parallel()

	page := report.Build(&report.Input{
		Timeline: fixture(t),
		Project:  "proj",
		Goal: &report.Goal{
			Sentence: "Memory reports its used fraction",
			Unmet:    []string{"docs mention UsedFraction"}, Total: 3, Done: 2,
		},
		State: &report.State{Changed: 2},
		Tally: &report.Tally{RouteHints: 1, RenameDenials: 1},
	})

	var out bytes.Buffer
	require.NoError(t, page.HTML(&out))
	golden(t, "session.html.golden", out.Bytes())
}

func TestPage_TextMatchesTheGoldenFile(t *testing.T) {
	t.Parallel()

	page := report.Build(&report.Input{Timeline: fixture(t), Project: "proj"})

	var out bytes.Buffer
	require.NoError(t, page.Text(&out))
	golden(t, "session.txt.golden", out.Bytes())
}

func titles(items []report.Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Title)
	}

	return out
}

func TestBuild_LeadsWithWhatNeedsThePerson(t *testing.T) {
	t.Parallel()

	page := report.Build(&report.Input{
		Timeline: fixture(t),
		Project:  "proj",
		Goal:     &report.Goal{Sentence: "ship it", Unmet: []string{"docs"}, Total: 2, Done: 1},
	})

	assert.Equal(t, []string{
		"NOT CHECKED: lint",
		"ALREADY FAILING: test",
		"Goal item not met: docs",
		"A test was skipped in internal/sysinfo/memory_test.go",
		"A test was removed in internal/sysinfo/memory_test.go",
		"Context ran high",
	}, titles(page.Needs))
	assert.Equal(t, []string{
		"1 file read more than once",
		"1 whole-project run through the shell",
		"A rename by text was denied and sent again",
	}, titles(page.Waste))

	assert.Equal(t, "need-3", page.Needs[2].ID)
	assert.Contains(t, page.Needs[2].Copy, "wavez goal status")
	assert.Equal(t,
		"The work is in internal/sysinfo/memory.go. Read a file once, in full, and start there.", page.Waste[0].Copy)
	assert.Equal(t, "wavez rename internal/sysinfo/memory.go:Read ReadLog", page.Waste[2].Copy)
	assert.Contains(t, page.Waste[1].Detail, "already run the checks")

	assert.Len(t, strings.Split(page.Summary, "\n"), 5)
	assert.Contains(t, page.Summary, "13 turns and 13 steps in 1m 07s, cost $0.432, model claude-test-1")
	assert.Equal(t, "2 of 2 done", report.Build(&report.Input{Goal: &report.Goal{Total: 2, Done: 2}}).Goal.Progress)
}

func TestBuild_NeverPrintsAZeroForWhatNobodyMeasured(t *testing.T) {
	t.Parallel()

	tl, err := transcript.Timeline(strings.NewReader(
		`{"type":"user","message":{"content":"hello"}}` + "\n" +
			`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}]}}` + "\n",
	))
	require.NoError(t, err)

	page := report.Build(&report.Input{Timeline: tl, Project: "proj"})

	value := func(facts []report.Fact, label string) string {
		for _, f := range facts {
			if f.Label == label {
				return f.Value
			}
		}

		return "missing " + label
	}

	assert.Equal(t, report.NotRecorded, page.Date)
	assert.Equal(t, report.NotRecorded, value(page.Stats, "Duration"))
	assert.Equal(t, report.NotRecorded, value(page.Stats, "Cost"))
	assert.Equal(t, report.NotRecorded, value(page.Stats, "Model"))
	assert.Equal(t, "1", value(page.Stats, "Turns"))
	assert.Equal(t, report.NotRecorded, value(page.Facts, "Input read from cache"))
	assert.Equal(t, report.NotRecorded, value(page.Facts, "Time in the model"))
	assert.Equal(t, report.NotRecorded, value(page.Facts, "Hook text handed to the agent"))
	assert.Equal(t, report.NotRecorded, page.Kinds[0].Output)
	assert.Empty(t, page.Needs)
	assert.Empty(t, page.Waste)
	assert.Nil(t, page.Goal)
	assert.Equal(t, "The transcript records no Stop hook run.", page.GateNote)

	var out bytes.Buffer
	require.NoError(t, page.HTML(&out))
	assert.NotContains(t, out.String(), "h-needs", "the section is left out when nothing needs the person")
}

// finding is a timeline and the titles the page should raise from it.
type finding struct {
	state *report.State
	name  string
	needs []string
	waste []string
	tl    transcript.SessionTimeline
}

func findings() []finding {
	gate := func(outcome transcript.GateOutcome, step int, checks ...transcript.GateCheck) transcript.GateRound {
		return transcript.GateRound{Outcome: outcome, Step: step, Checks: checks, Headline: "FAIL build"}
	}
	failing := transcript.GateCheck{Name: "build", Status: "fail"}
	work := func(kinds ...transcript.Kind) []transcript.Step {
		out := make([]transcript.Step, 0, len(kinds))
		for i, k := range kinds {
			out = append(out, transcript.Step{Kind: k, Index: i, Label: string(k)})
		}

		return out
	}

	return []finding{
		{
			name: "a release is reported once and not again as a session that ended failing",
			tl: transcript.SessionTimeline{
				Steps: work(transcript.KindEdit, transcript.KindHook, transcript.KindHook),
				Gates: []transcript.GateRound{
					gate(transcript.GateBlock, 1, failing), gate(transcript.GateReleased, 2, failing),
				},
			},
			needs: []string{"The checks gave up on a failure"},
		},
		{
			name: "a block with nothing after it is a session that ended failing",
			tl: transcript.SessionTimeline{
				Steps: work(transcript.KindEdit, transcript.KindHook),
				Gates: []transcript.GateRound{gate(transcript.GateBlock, 1, failing)},
			},
			needs: []string{"The session ended while checks were failing"},
		},
		{
			name: "a block the agent answered is not",
			tl: transcript.SessionTimeline{
				Steps: work(transcript.KindEdit, transcript.KindHook, transcript.KindEdit),
				Gates: []transcript.GateRound{gate(transcript.GateBlock, 1, failing)},
			},
		},
		{
			name:  "wavez's own count of failed rounds says so when the transcript does not",
			state: &report.State{FailingRounds: 2},
			needs: []string{"The session ended while checks were failing"},
		},
		{
			name: "a hook that could not run abstains and the person hears it",
			tl: transcript.SessionTimeline{
				Steps: work(transcript.KindHook),
				Gates: []transcript.GateRound{{Outcome: transcript.GateError, Headline: "wavez hook: boom"}},
			},
			needs: []string{"A Stop hook could not run"},
		},
		{
			name: "a long look around before the first edit, and a long tail after the last",
			tl: transcript.SessionTimeline{
				Edits: 1, RetrievalBeforeEdit: 8, TurnsAfterLastEdit: 4, Edited: []string{"a.go"},
			},
			waste: []string{"8 retrieval steps before the first edit", "4 turns after the last edit"},
		},
		{
			name: "short of either threshold is not waste",
			tl:   transcript.SessionTimeline{Edits: 1, RetrievalBeforeEdit: 7, TurnsAfterLastEdit: 3},
		},
		{
			name: "a long retrieval run counts when nothing was edited",
			tl: transcript.SessionTimeline{
				LongestRetrieval: transcript.Run{First: 2, Length: 9}, TurnsAfterLastEdit: 9,
			},
			waste: []string{"9 retrieval steps in a row"},
		},
		{
			name: "a sweep with no sign of wavez's checks points at init",
			tl: transcript.SessionTimeline{Sweeps: []transcript.Sweep{
				{Command: "go test", Step: 1}, {Command: "go build", Step: 1}, {Command: "go test", Step: 4},
			}},
			waste: []string{"2 whole-project runs through the shell"},
		},
		{
			name:  "a denial that was not retried is still worth a sentence",
			tl:    transcript.SessionTimeline{Denials: []transcript.Denial{{Step: 3}}},
			waste: []string{"A rename by text was denied"},
		},
	}
}

func TestBuild_Findings(t *testing.T) {
	t.Parallel()

	tests := findings()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			page := report.Build(&report.Input{Timeline: tt.tl, State: tt.state, Project: "proj"})
			assert.Equal(t, tt.needs, nilIfEmpty(titles(page.Needs)))
			assert.Equal(t, tt.waste, nilIfEmpty(titles(page.Waste)))

			for _, item := range page.Waste {
				assert.NotEmpty(t, item.Copy, "every piece of waste comes with something to paste")
			}
		})
	}
}

func nilIfEmpty(items []string) []string {
	if len(items) == 0 {
		return nil
	}

	return items
}

func TestBuild_SweepWithoutWavezSuggestsInit(t *testing.T) {
	t.Parallel()

	tl := transcript.SessionTimeline{Sweeps: []transcript.Sweep{{Command: "go test", Step: 1}}}

	assert.Equal(t, "wavez init", report.Build(&report.Input{Timeline: tl}).Waste[0].Copy)
	wired := report.Build(&report.Input{Timeline: tl, State: &report.State{}})
	assert.Contains(t, wired.Waste[0].Copy, "Do not run go test")
}

func TestPage_HTMLEscapesWhatTheSessionSaid(t *testing.T) {
	t.Parallel()

	tl := transcript.SessionTimeline{
		Start: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		Steps: []transcript.Step{{
			Kind: transcript.KindShell, Label: `Bash echo "</button><script>alert(1)</script>"`,
			Detail: `<img src=x onerror=alert(2)>`,
		}},
		Turns:       []transcript.Turn{{Kind: transcript.KindShell}},
		RepeatReads: []transcript.FileCount{{Path: `"><script>alert(3)</script>.go`, Count: 2}},
	}

	page := report.Build(&report.Input{Timeline: tl, Project: "<b>proj</b>", Full: true})

	var out bytes.Buffer
	require.NoError(t, page.HTML(&out))

	html := out.String()
	assert.Equal(t, 1, strings.Count(html, "<script>"), "only the page's own script")
	assert.NotContains(t, html, "<img")
	assert.NotContains(t, html, "<b>proj</b>")
	assert.Contains(t, html, "&lt;b&gt;proj&lt;/b&gt;")
	assert.Contains(t, html, `data-detail="&lt;img src=x onerror=alert(2)&gt;"`)
	assert.NotContains(t, html, "ZgotmplZ", "a width the template refused would break the bar")
	assert.NotContains(t, html, "http://")
	assert.NotContains(t, html, "https://")
}

func TestPage_DetailIsOnTheFullPageOnly(t *testing.T) {
	t.Parallel()

	render := func(full bool) string {
		page := report.Build(&report.Input{Timeline: fixture(t, transcript.WithDetail()), Project: "proj", Full: full})

		var out bytes.Buffer
		require.NoError(t, page.HTML(&out))

		return out.String()
	}

	assert.NotContains(t, render(false), "data-detail=")
	assert.NotContains(t, render(false), "--- FAIL: TestUsedFraction")
	assert.Contains(t, render(true), `data-detail="go build ./... &amp;&amp; go test ./..."`)
	assert.Contains(t, render(true), "--- FAIL: TestUsedFraction")
}
