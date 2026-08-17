package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

func sampleThreads() []api.ThreadInfo {
	base := fixedNow().Add(-time.Hour)

	return []api.ThreadInfo{
		{
			ID: "t1", Name: "fix-lock-timeout", Dir: "calcipy", Step: "editing internal/lease.go",
			State: event.StateWorking, LastEvent: base.Add(2 * time.Minute),
		},
		{
			ID: "t2", Name: "docs-pass", Dir: "calcipy", Step: "allow? rm -rf .testmondata",
			State: event.StateNeedsIn, LastEvent: base.Add(40 * time.Second),
		},
		{
			ID: "t3", Name: "add-jj-backend", Dir: "wavez", Step: "gate test 4/7",
			State: event.StateGating, LastEvent: base.Add(time.Minute),
		},
		{
			ID: "t4", Name: "flaky-ci", Dir: "yak-shears", Step: "go test failed",
			State: event.StateFailed, LastEvent: base,
		},
		{
			ID: "t5", Name: "release-notes", Dir: "yak-shears", Step: "done",
			State: event.StateDone, LastEvent: base.Add(-time.Hour),
		},
	}
}

func TestHome_RendersEveryState(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}} {
		m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, size.w, size.h)
		m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

		out := m.View().Content

		for _, want := range []string{"fix-lock-timeout", "docs-pass", "add-jj-backend", "flaky-ci", "release-notes"} {
			assert.Contains(t, out, want, "size %dx%d", size.w, size.h)
		}

		for _, glyph := range []string{">", "*", "!", "x", "ok"} {
			assert.Contains(t, out, glyph, "size %dx%d missing ascii glyph %q", size.w, size.h, glyph)
		}
	}
}

func TestHome_SortsNeedsInputFirst(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	out := m.View().Content

	needsInput := strings.Index(out, "docs-pass")
	working := strings.Index(out, "fix-lock-timeout")
	done := strings.Index(out, "release-notes")

	assert.Positive(t, needsInput)
	assert.Less(t, needsInput, working, "needs-input thread should render before a working thread")
	assert.Less(t, working, done, "more recent threads should render before older ones")
}

func TestHome_EmptyState(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{}, 80, 24)
	out := m.View().Content

	assert.Contains(t, out, "no threads yet · press n to start one")
}

func TestHome_SingleThread(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 80, 24)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()[:1]})

	out := m.View().Content
	assert.Contains(t, out, "fix-lock-timeout")
	assert.Contains(t, out, "1 threads")
}

func TestHome_GoldenFrame(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, 80, 24)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	goldenCompare(t, "home_frame", m.View().Content)
}

// TestHome_PermissionRowAnsweredInline covers the expand-and-answer UI path;
// TestPermissionAnswer_SendsDecisionInline (internal_test.go) covers that
// the y/n/a keys actually dispatch the right decision to the client.
func TestHome_PermissionRowAnsweredInline(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = apply(t, m,
		api.Reply{Kind: api.RepThreads, Threads: sampleThreads()},
		api.Reply{Kind: api.RepPending, Pending: []api.PendingInfo{
			{ID: "p1", ThreadID: "t2", Thread: "docs-pass", Tool: "shell", Action: "rm -rf .testmondata"},
		}},
	)

	m = apply(t, m, tea.KeyPressMsg{Code: 'v', Text: "v"})
	out := m.View().Content

	assert.Contains(t, out, "rm -rf .testmondata")
	assert.Contains(t, out, "[y]es [n]o [a]lways")
}
