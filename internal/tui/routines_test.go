package tui_test

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/tui"
)

func sampleRoutines() []api.RoutineInfo {
	base := fixedNow().Add(-30 * time.Minute)

	return []api.RoutineInfo{
		{
			Name: "gate-format", Triggers: []string{"manual"}, Steps: []string{"format"}, Enabled: true,
			Runs: []api.RoutineRun{
				{Started: base, Trigger: "manual", Duration: 120 * time.Millisecond, Pass: true},
				{Started: base.Add(10 * time.Minute), Trigger: "manual", Duration: 90 * time.Millisecond, Pass: true},
				{Started: base.Add(25 * time.Minute), Trigger: "manual", Duration: 240 * time.Millisecond, Pass: true},
			},
		},
		{
			Name: "gate-go-test", Triggers: []string{"manual"}, Steps: []string{"go-test"}, Enabled: false,
		},
		{
			Name: "nightly-audit", Triggers: []string{"schedule", "manual"}, Steps: []string{"vet", "test"},
			Enabled: true,
			Runs: []api.RoutineRun{
				{Started: base.Add(2 * time.Minute), Trigger: "schedule", Duration: 9 * time.Second, Pass: true},
				{
					Started: base.Add(20 * time.Minute), Trigger: "schedule", Duration: 71 * time.Second,
					Failed: []string{"test fail"},
				},
			},
		},
	}
}

func routinesModel(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, width, height)

	return apply(t, m,
		tea.KeyPressMsg{Code: 'R', Text: "R"},
		api.Reply{Kind: api.RepRoutines, Routines: sampleRoutines()},
	)
}

func TestRoutines_RendersTriggersLastRunAndSparkline(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}} {
		m := routinesModel(t, size.w, size.h)
		out := m.View().Content

		for _, want := range []string{"gate-format", "gate-go-test", "nightly-audit", "schedule,manual", "[r]run"} {
			assert.Contains(t, out, want, "size %dx%d", size.w, size.h)
		}

		assert.Contains(t, out, "+=@",
			"size %dx%d: the sparkline scales per routine and degrades to ascii under NO_COLOR", size.w, size.h)

		goldenCompare(t, fmt.Sprintf("routines_frame_%dx%d", size.w, size.h), out)
	}
}

func TestRoutines_HistoryExpandsUnderTheCursor(t *testing.T) {
	t.Parallel()

	m := routinesModel(t, 100, 30)
	m = apply(t, m,
		tea.KeyPressMsg{Code: 'j', Text: "j"},
		tea.KeyPressMsg{Code: 'j', Text: "j"},
		tea.KeyPressMsg{Code: 'h', Text: "h"},
	)

	out := m.View().Content
	assert.Contains(t, out, "test fail", "history names the step that failed")
	assert.Contains(t, out, "1m11s", "a run over a minute reads in minutes and seconds")
	assert.Contains(t, out, "9.0s")
}

func TestRoutines_EmptyState(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 80, 24)
	m = apply(t, m, tea.KeyPressMsg{Code: 'R', Text: "R"})

	assert.Contains(t, m.View().Content, "no routines · define one in .wavez.pkl")
}

func TestRoutines_EscReturnsHome(t *testing.T) {
	t.Parallel()

	m := routinesModel(t, 80, 24)
	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	assert.NotContains(t, m.View().Content, "routines ·")
}
