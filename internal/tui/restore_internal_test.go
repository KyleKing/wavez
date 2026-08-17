package tui

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

var errRestoreFailed = errors.New("restore left the working copy changed")

const stat = "internal/lease/lease.go | 8 ++++----\n1 files changed, 4 insertions(+), 4 deletions(-)\n"

func restoreFixture(t *testing.T, fc *fakeClient, checkpoint string) Model {
	t.Helper()

	m := New(Options{Now: func() time.Time { return time.Unix(0, 0) }})
	m.client = fc

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	rm, ok := resized.(Model)
	require.True(t, ok)

	m = rm
	m.applyReply(api.Reply{Kind: api.RepThreads, Threads: []api.ThreadInfo{
		{ID: "t1", Name: "fix-lock", Dir: "wavez", State: event.StateWorking, Checkpoint: checkpoint},
	}})
	m.thread.activeID = "t1"
	m.stack = append(m.stack, screenThread)

	return m
}

// `u` asks what an undo costs and never restores on its own; only the
// confirmation sends the destructive command.
func TestRestoreKey_ConfirmsBeforeRestoring(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := restoreFixture(t, fc, "op-abc")

	m = pressKey(t, m, 'u')
	require.Equal(t, []restoreCall{{threadID: "t1"}}, fc.restores)
	assert.False(t, m.restore.open, "the prompt opens on the daemon's answer, not on the keypress")

	m.applyReply(api.Reply{Kind: api.RepRestore, Restore: &api.Restore{
		ThreadID: "t1", Checkpoint: "op-abc", Summary: stat,
	}})
	require.True(t, m.restore.open)
	assert.Contains(t, m.render(), "internal/lease/lease.go")

	m = pressKey(t, m, 'y')
	require.Len(t, fc.restores, 2)
	assert.Equal(t, restoreCall{threadID: "t1", confirm: true}, fc.restores[1])

	m.applyReply(api.Reply{Kind: api.RepRestore, Restore: &api.Restore{
		ThreadID: "t1", Checkpoint: "op-abcdef0123456", Summary: stat, Restored: true,
	}})
	assert.False(t, m.restore.open)
	assert.Contains(t, m.status, "restored to checkpoint op-abcdef012")
	assert.Contains(t, m.status, "1 files changed")
}

// Refusing leaves the tree alone, however the refusal is spelled.
func TestRestoreKey_RefusePathsSendNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		named string
		key   rune
	}{
		{name: "n cancels", key: 'n'},
		{name: "esc cancels", named: keyEsc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := &fakeClient{}
			m := restoreFixture(t, fc, "op-abc")
			m.applyReply(api.Reply{Kind: api.RepRestore, Restore: &api.Restore{
				ThreadID: "t1", Checkpoint: "op-abc", Summary: stat,
			}})

			if tc.named != "" {
				m = pressKey(t, m, 0, tc.named)
			} else {
				m = pressKey(t, m, tc.key)
			}

			assert.False(t, m.restore.open)
			assert.Empty(t, fc.restores, "a refused undo sends no command")
			assert.Equal(t, "undo canceled", m.status)
		})
	}
}

// A thread that never ran has nothing to go back to, and a daemon refusal
// says so rather than reading as a completed undo.
func TestRestore_FailuresAreReported(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := restoreFixture(t, fc, "")

	m = pressKey(t, m, 'u')
	assert.Empty(t, fc.restores, "a thread with no checkpoint sends no command")
	assert.Equal(t, "fix-lock has no checkpoint yet", m.status)

	m.applyReply(api.Reply{Kind: api.RepError, Error: "daemon: nothing has changed since the checkpoint"})
	assert.Equal(t, "daemon: nothing has changed since the checkpoint", m.status)

	updated, _ := m.Update(restoreErrMsg{err: errRestoreFailed})

	um, ok := updated.(Model)
	require.True(t, ok)
	assert.Contains(t, um.status, "undo failed: restore left the working copy changed")
	assert.False(t, um.restore.open)
}
