package tui

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

// toastSizes spans DESIGN's 80x24 minimum and a wide terminal, since the
// toast overlays whatever footer the screen at that size already renders.
var toastSizes = []struct{ w, h int }{{80, 24}, {120, 40}}

func sizedModel(t *testing.T, w, h int) Model {
	t.Helper()

	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})

	resized, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	rm, ok := resized.(Model)
	require.True(t, ok)

	return rm
}

// applyM folds msgs through m.Update and returns the concrete Model, mirroring
// the tui_test package's apply helper for this package's white-box tests.
func applyM(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()

	tm := tea.Model(m)
	for _, msg := range msgs {
		tm, _ = tm.Update(msg)
	}

	rm, ok := tm.(Model)
	require.True(t, ok)

	return rm
}

func TestToast_TransitionRaisesToastWithGlyphAndText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  string
		from  api.ThreadInfo
		to    api.ThreadInfo
		fleet bool
	}{
		{
			name:  "needs input in the fleet scope",
			from:  api.ThreadInfo{ID: "t1", Root: "/dev/calcipy", Name: "docs-pass", State: event.StateWorking},
			to:    api.ThreadInfo{ID: "t1", Root: "/dev/calcipy", Name: "docs-pass", State: event.StateNeedsIn},
			want:  "▲ calcipy/docs-pass needs input",
			fleet: true,
		},
		{
			name:  "done in the fleet scope",
			from:  api.ThreadInfo{ID: "t1", Root: "/dev/wavez", Name: "fix-lock", State: event.StateWorking},
			to:    api.ThreadInfo{ID: "t1", Root: "/dev/wavez", Name: "fix-lock", State: event.StateDone},
			want:  "✔ wavez/fix-lock done",
			fleet: true,
		},
		{
			name: "failed with a verify_failed step",
			from: api.ThreadInfo{ID: "t1", Name: "add-jj-backend", State: event.StateWorking},
			to:   api.ThreadInfo{ID: "t1", Name: "add-jj-backend", State: event.StateFailed, Step: "verify_failed"},
			want: "✖ add-jj-backend verify_failed",
		},
		{
			name: "failed with a tripped-bound step",
			from: api.ThreadInfo{ID: "t1", Name: "flaky-ci", State: event.StateWorking},
			to:   api.ThreadInfo{ID: "t1", Name: "flaky-ci", State: event.StateFailed, Step: "hit max turns"},
			want: "✖ flaky-ci hit max turns",
		},
		{
			name: "failed with no step falls back to a fixed phrase",
			from: api.ThreadInfo{ID: "t1", Name: "flaky-ci", State: event.StateWorking},
			to:   api.ThreadInfo{ID: "t1", Name: "flaky-ci", State: event.StateFailed},
			want: "✖ flaky-ci failed",
		},
		{
			name: "root-qualified thread stays bare-named outside the fleet scope",
			from: api.ThreadInfo{ID: "t1", Root: "/dev/wavez", Name: "add-jj-backend", State: event.StateWorking},
			to:   api.ThreadInfo{ID: "t1", Root: "/dev/wavez", Name: "add-jj-backend", State: event.StateFailed},
			want: "✖ add-jj-backend failed",
		},
	}

	for _, tc := range tests {
		for _, size := range toastSizes {
			t.Run(tc.name+"_"+strconv.Itoa(size.w), func(t *testing.T) {
				t.Parallel()

				m := sizedModel(t, size.w, size.h)
				if tc.fleet {
					m = applyM(t, m, tea.KeyPressMsg{Code: 'w', Text: "w"})
				}

				m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{tc.from}})
				m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{tc.to}})

				require.Equal(t, tc.want, m.toast.current)
				require.Contains(t, m.render(), tc.want)
			})
		}
	}
}

func TestToast_FirstSightingDoesNotToast(t *testing.T) {
	t.Parallel()

	m := sizedModel(t, 80, 24)
	m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", State: event.StateNeedsIn},
	}})

	require.Empty(t, m.toast.current)
	require.Empty(t, m.toast.queue)
}

func TestToast_OpenThreadNeedsInputDoesNotToastButOthersDo(t *testing.T) {
	t.Parallel()

	m := sizedModel(t, 100, 30)
	m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", State: event.StateWorking},
		{ID: "t2", Name: "add-jj-backend", State: event.StateWorking},
	}})
	m.thread.activeID = "t1"
	m.push(screenThread)

	m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", State: event.StateNeedsIn},
		{ID: "t2", Name: "add-jj-backend", State: event.StateWorking},
	}})
	require.Empty(t, m.toast.current, "the open thread's own needs-input must not toast")

	m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", State: event.StateNeedsIn},
		{ID: "t2", Name: "add-jj-backend", State: event.StateNeedsIn},
	}})
	require.Equal(t, "▲ add-jj-backend needs input", m.toast.current, "a different thread's needs-input still toasts")
}

func TestToast_QueueShowsOneAtATime(t *testing.T) {
	t.Parallel()

	m := sizedModel(t, 80, 24)
	m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", State: event.StateWorking},
		{ID: "t2", Name: "flaky-ci", State: event.StateWorking},
	}})

	m = applyM(t, m, api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", State: event.StateNeedsIn},
		{ID: "t2", Name: "flaky-ci", State: event.StateDone},
	}})

	first := m.toast.current
	require.NotEmpty(t, first)
	require.Len(t, m.toast.queue, 1, "the second transition queues instead of overwriting the first")

	m.dismissToast()
	m, _ = m.advanceToast()

	require.NotEqual(t, first, m.toast.current)
	require.Empty(t, m.toast.queue)
}

// docsPassGoesNeedsInput folds a working-to-needs-input transition for a
// single "docs-pass" thread through m, the fixture every single-toast test
// below raises its toast from.
func docsPassGoesNeedsInput(t *testing.T, m Model) Model {
	t.Helper()

	working := api.ThreadInfo{ID: "t1", Name: "docs-pass", State: event.StateWorking}
	needsInput := api.ThreadInfo{ID: "t1", Name: "docs-pass", State: event.StateNeedsIn}

	return applyM(t, m,
		api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{working}},
		api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{needsInput}},
	)
}

func TestToast_TimerClearsToast(t *testing.T) {
	t.Parallel()

	m := docsPassGoesNeedsInput(t, sizedModel(t, 80, 24))
	require.NotEmpty(t, m.toast.current)

	gen := m.toast.gen

	m = applyM(t, m, toastTickMsg{gen: gen - 1})
	require.NotEmpty(t, m.toast.current, "a stale tick from an earlier toast must not clear the current one")

	m = applyM(t, m, toastTickMsg{gen: gen})
	require.Empty(t, m.toast.current)
}

func TestToast_KeypressClearsToastWithoutStealingTheKey(t *testing.T) {
	t.Parallel()

	m := docsPassGoesNeedsInput(t, sizedModel(t, 100, 30))
	require.NotEmpty(t, m.toast.current)

	m = applyM(t, m, tea.KeyPressMsg{Code: 'i', Text: "i"})

	require.Empty(t, m.toast.current, "any keypress dismisses the toast")
	require.Equal(t, screenInbox, m.top(), "the key that dismissed the toast still did its own job")
}

func TestToast_NoColorUsesASCIIGlyph(t *testing.T) {
	t.Parallel()

	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }, NoColor: true})

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rm, ok := resized.(Model)
	require.True(t, ok)

	rm = docsPassGoesNeedsInput(t, rm)

	require.Equal(t, "! docs-pass needs input", rm.toast.current)
	require.Contains(t, rm.render(), "! docs-pass needs input")
	require.NotContains(t, rm.render(), "▲")
}
