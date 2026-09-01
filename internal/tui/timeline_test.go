package tui_test

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

// timelineEvents is a run whose shape the screen has to show: three turns,
// the middle one the longest so the bars scale against it, and the last one
// over toolsPerRow so its cursor row has a tool list to expand.
func timelineEvents() []event.Event {
	base := fixedNow().Add(-10 * time.Minute)

	ev := func(offset time.Duration, k event.Kind, mut func(*event.Event)) event.Event {
		out := event.Event{Kind: k, At: base.Add(offset)}
		if mut != nil {
			mut(&out)
		}

		return out
	}

	tool := func(name string, isErr bool, cause string) func(*event.Event) {
		return func(e *event.Event) {
			e.Tool = name
			e.Detail = map[string]any{"is_error": isErr, "cause": cause}
		}
	}

	answer := func(e *event.Event) { e.Role = event.RoleAnswer }

	says := func(text string) func(*event.Event) {
		return func(e *event.Event) { e.Text = text }
	}

	turn := func(start, span time.Duration, tools ...event.Event) []event.Event {
		out := slices.Clone(tools)
		out[0].At = base.Add(start)
		out = append(out,
			ev(start+span, event.KindAgent, says("done")),
			ev(start+span, event.KindAgent, answer),
		)

		return out
	}

	t := func(offset time.Duration, name string, isErr bool, cause string) event.Event {
		return ev(offset, event.KindTool, tool(name, isErr, cause))
	}

	return slices.Concat(
		turn(0, 1*time.Minute,
			t(0, "read", false, ""),
			t(20*time.Second, "edit", false, ""),
		),
		turn(2*time.Minute, 4*time.Minute,
			t(2*time.Minute, "shell", true, "exit 1"),
			t(3*time.Minute, "edit", false, ""),
			t(4*time.Minute, "read", false, ""),
			t(5*time.Minute, "read", false, ""),
		),
		turn(6*time.Minute, 30*time.Second,
			t(6*time.Minute, "read", false, ""),
			t(6*time.Minute+5*time.Second, "read", false, ""),
			t(6*time.Minute+10*time.Second, "read", false, ""),
			t(6*time.Minute+15*time.Second, "read", false, ""),
			t(6*time.Minute+20*time.Second, "read", false, ""),
			t(6*time.Minute+25*time.Second, "edit", false, ""),
			t(6*time.Minute+28*time.Second, "edit", false, ""),
		),
	)
}

func openTimeline(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, width, height)
	m = openThread(t, m, sampleThreads()[:1])

	for i := range timelineEvents() {
		ev := timelineEvents()[i]
		ev.ThreadID = "t1"
		m = apply(t, m, api.Reply{Kind: api.RepEvent, Event: &ev})
	}

	return apply(t, m, tea.KeyPressMsg{Code: 'p', Text: "p"})
}

func TestTimeline_GoldenFrame(t *testing.T) {
	t.Parallel()

	goldenCompare(t, "timeline_80x24", openTimeline(t, 80, 24).View().Content)
}

func TestTimeline_EscReturnsToThread(t *testing.T) {
	t.Parallel()

	m := apply(t, openTimeline(t, 100, 30), tea.KeyPressMsg{Code: tea.KeyEsc})

	assert.Contains(t, m.View().Content, "fix-lock-timeout")
}
