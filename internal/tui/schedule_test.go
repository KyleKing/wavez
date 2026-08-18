package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

func cells(states ...event.State) []event.State {
	out := make([]event.State, 0, 15)
	for len(out) < 15 {
		out = append(out, states[len(out)%len(states)])
	}

	return out
}

func sampleSchedule() api.Schedule {
	const total = 16 << 30

	return api.Schedule{
		Phase:         "execute",
		LocalModel:    "qwen3:8b",
		Headroom:      0.25,
		MemMeasured:   true,
		MemUsedBytes:  total - (5 << 30),
		MemTotalBytes: total,
		Lanes: []api.Lane{
			{
				ThreadID: "t1", Thread: "fix-lock-timeout", Step: "editing internal/lease.go",
				Cells: cells(event.StateWorking),
			},
			{
				ThreadID: "t3", Thread: "add-jj-backend", Step: "gate test 4/7", Gate: "gate test 4/7",
				Cells: cells(event.StateWorking, event.StateGating),
			},
			{
				ThreadID: "t6", Thread: "jj-op-log-undo", Step: "waiting lock internal/vcs ← add-jj-backend",
				Lock: "internal/vcs", LockHolder: "add-jj-backend", Cells: cells(event.StateBlocked),
			},
			{ThreadID: "t2", Thread: "docs-pass", Step: "needs input", Cells: cells(event.StateNeedsIn)},
		},
		Leases: []api.Lease{
			{Subtree: "internal/vcs", Holder: "add-jj-backend", State: "active", Waiters: []string{"jj-op-log-undo"}},
			{Subtree: "internal/tui", Holder: "docs-pass", State: "committed"},
		},
	}
}

func openSchedule(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, width, height)
	m = apply(t, m,
		api.Reply{Kind: api.RepThreads, Threads: sampleThreads()},
		api.Reply{Kind: api.RepSchedule, Schedule: scheduleReply()},
		tea.KeyPressMsg{Code: 's', Text: "s"},
	)

	return m
}

func scheduleReply() *api.Schedule {
	s := sampleSchedule()

	return &s
}

func TestSchedule_GoldenFrames(t *testing.T) {
	t.Parallel()

	for _, size := range []struct {
		name string
		w, h int
	}{{name: "schedule_80x24", w: 80, h: 24}, {name: "schedule_120x40", w: 120, h: 40}} {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()
			goldenCompare(t, size.name, openSchedule(t, size.w, size.h).View().Content)
		})
	}
}

// A lock wait has to name the holder, since "blocked" alone does not say who
// to go look at.
func TestSchedule_LockWaitNamesTheHolder(t *testing.T) {
	t.Parallel()

	out := openSchedule(t, 120, 40).View().Content

	assert.Contains(t, out, "lock internal/vcs <- add-jj-backend")
	assert.Contains(t, out, "phase: execute")
	assert.Contains(t, out, "qwen3:8b loaded")
}

func TestSchedule_LeaseListBehindL(t *testing.T) {
	t.Parallel()

	m := apply(t, openSchedule(t, 120, 40), tea.KeyPressMsg{Code: 'l', Text: "l"})
	out := m.View().Content

	assert.Contains(t, out, "internal/vcs")
	assert.Contains(t, out, "committed")
	assert.Contains(t, out, "jj-op-log-undo")
}

func TestSchedule_EscReturnsHome(t *testing.T) {
	t.Parallel()

	m := apply(t, openSchedule(t, 100, 30), tea.KeyPressMsg{Code: tea.KeyEsc})

	assert.Contains(t, m.View().Content, "[enter]open")
	assert.NotContains(t, m.View().Content, "phase:")
}
