package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/router"
)

func openedThread(t *testing.T, fc *fakeClient, info api.ThreadInfo) Model {
	t.Helper()

	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	rm, ok := resized.(Model)
	require.True(t, ok)

	m = rm
	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{info}})
	m.thread.activeID = info.ID
	m.push(screenThread)

	return m
}

// A pin is only safe if clearing it is reachable, so the cycle has to come
// back round to automatic routing rather than only alternate tiers.
func TestRoute_CycleReachesEveryTierAndBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current router.Choice
		want    router.Choice
	}{
		{name: "auto pins fast", current: "", want: router.ChoiceFast},
		{name: "fast pins balanced", current: router.ChoiceFast, want: router.ChoiceBalanced},
		{name: "balanced pins deep", current: router.ChoiceBalanced, want: router.ChoiceDeep},
		{name: "deep clears the pin", current: router.ChoiceDeep, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := &fakeClient{}
			m := openedThread(t, fc, api.ThreadInfo{ID: "t1", Name: "fix-lock", Dir: "wavez", Override: tc.current})

			got, _ := m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
			_, ok := got.(Model)
			require.True(t, ok)

			require.Len(t, fc.routes, 1)
			assert.Equal(t, routeCall{threadID: "t1", override: tc.want}, fc.routes[0])
		})
	}
}

func TestRoute_PaletteVerbsPinAndClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		verb string
		want router.Choice
	}{
		{name: "auto", verb: verbRouteAuto, want: ""},
		{name: "fast", verb: verbRouteFast, want: router.ChoiceFast},
		{name: "balanced", verb: verbRouteBalanced, want: router.ChoiceBalanced},
		{name: "deep", verb: verbRouteDeep, want: router.ChoiceDeep},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := &fakeClient{}
			m := openedThread(t, fc, api.ThreadInfo{ID: "t1", Name: "fix-lock", Dir: "wavez"})

			mm, _ := m.runPaletteEntry(paletteEntry{kind: kindVerb, target: tc.verb})
			assert.False(t, mm.palette.open)

			require.Len(t, fc.routes, 1)
			assert.Equal(t, routeCall{threadID: "t1", override: tc.want}, fc.routes[0])
		})
	}
}

// A pinned tier is never escalated away from, so a run that fails on one
// stops there. Without this the frame says "failed" and nothing says why.
func TestRoute_FailureOnAPinnedTierSaysHowToGetOff(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := openedThread(t, fc, api.ThreadInfo{
		ID: "t1", Name: "fix-lock", Dir: "wavez", Override: router.ChoiceFast, State: event.StateWorking,
	})

	failed := api.ThreadInfo{
		ID: "t1", Name: "fix-lock", Dir: "wavez", Override: router.ChoiceFast, State: event.StateFailed, Seq: 12,
	}

	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{failed}})
	assert.Contains(t, m.status, "pinned to fast")

	m.status = ""
	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{failed}})
	assert.Empty(t, m.status, "a repeated poll of the same failure must not re-announce it")

	// A second run that fails before any poll sees it working never leaves
	// the failed state, and still has to be announced.
	next := failed
	next.Seq = 19
	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{next}})
	assert.Contains(t, m.status, "pinned to fast")
}

// The thinking cycle starts at off because that is the setting that pays:
// measured on qwen3:8b, replying "OK" costs 79 completion tokens with the
// reasoning trace on and 2 with it off.
func TestThinkingCycle(t *testing.T) {
	t.Parallel()

	off, on := false, true

	tests := []struct {
		current *bool
		want    *bool
		name    string
	}{
		{name: "the model default turns it off first", current: nil, want: &off},
		{name: "off turns it on", current: &off, want: &on},
		{name: "on returns to the model default", current: &on, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nextThinking(tt.current)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("nextThinking() = %v, want nil", *got)
			case tt.want != nil && got == nil:
				t.Errorf("nextThinking() = nil, want %v", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("nextThinking() = %v, want %v", *got, *tt.want)
			}
		})
	}
}
