package sched_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/sched"
	"github.com/kyleking/wavez/internal/sysinfo"
)

const gb = 1 << 30

func memory(freeGB float64) func(context.Context) (sysinfo.Memory, error) {
	const total = 16 * gb

	return func(context.Context) (sysinfo.Memory, error) {
		return sysinfo.Memory{TotalBytes: total, UsedBytes: total - uint64(freeGB*gb)}, nil
	}
}

// The measured case: qwen3:8b loaded leaves ~31% free and a suite may run
// beside it, while a fuller machine serializes the two.
func TestAdmissionOverlapsOnlyWithHeadroom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		freeGB     float64
		wantHeldup bool
	}{
		{name: "qwen3:8b headroom", freeGB: 5, wantHeldup: false},
		{name: "gemma4:12b headroom", freeGB: 2.5, wantHeldup: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			holds := make(chan sched.Hold, 4)
			s := sched.New(sched.WithMemory(memory(tc.freeGB)))
			s.OnHold(func(h sched.Hold) { holds <- h })

			releaseGate, err := s.AdmitGate(t.Context())
			require.NoError(t, err)

			assert.Equal(t, sched.PhaseExecute, s.Snapshot(t.Context()).Phase)

			admitted := make(chan struct{})

			go func() {
				release, aErr := s.AdmitTurn(t.Context(), "alpha")
				if aErr == nil {
					release()
				}
				close(admitted)
			}()

			if !tc.wantHeldup {
				<-admitted
				releaseGate()

				return
			}

			held := <-holds
			assert.True(t, held.Held)
			assert.Equal(t, "alpha", held.Holder)
			assert.Contains(t, held.Reason, "gate run")

			releaseGate()
			<-admitted
			assert.False(t, (<-holds).Held)
		})
	}
}

// A machine wavez cannot measure must not become a machine nothing runs on.
func TestUnreadableMemoryAdmitsEverything(t *testing.T) {
	t.Parallel()

	s := sched.New(sched.WithMemory(func(context.Context) (sysinfo.Memory, error) {
		return sysinfo.Memory{}, assert.AnError
	}))

	releaseGate, err := s.AdmitGate(t.Context())
	require.NoError(t, err)

	releaseTurn, err := s.AdmitTurn(t.Context(), "alpha")
	require.NoError(t, err)

	releaseTurn()
	releaseGate()

	snap := s.Snapshot(t.Context())
	assert.False(t, snap.MemoryMeasured)
	assert.Equal(t, sched.PhaseEdit, snap.Phase)
}

func TestNilSchedulerAdmits(t *testing.T) {
	t.Parallel()

	var s *sched.Scheduler

	release, err := s.AdmitTurn(context.Background(), "alpha")
	require.NoError(t, err)
	release()
	assert.Equal(t, sched.PhaseEdit, s.Snapshot(context.Background()).Phase)
}

// Three threads sharing one llama-server slot is the measured case: all
// three were admitted, all three read as working, and each got one turn in
// three minutes before its deadline. Memory is plentiful here on purpose,
// since the slot bound is structural rather than a memory decision.
func TestLocalSlotsBoundConcurrentRequests(t *testing.T) {
	t.Parallel()

	holds := make(chan sched.Hold, 4)
	s := sched.New(sched.WithMemory(memory(12)), sched.WithLocalSlots(1))
	s.OnHold(func(h sched.Hold) { holds <- h })

	release, err := s.AdmitSlot(t.Context(), "alpha")
	require.NoError(t, err)

	admitted := make(chan struct{})

	go func() {
		r, aErr := s.AdmitSlot(t.Context(), "beta")
		if aErr == nil {
			r()
		}
		close(admitted)
	}()

	held := <-holds
	assert.True(t, held.Held)
	assert.Equal(t, "beta", held.Holder)
	assert.Contains(t, held.Reason, "slot")

	assert.Equal(t, 1, s.Snapshot(t.Context()).SlotsHeld)

	release()
	<-admitted
	assert.False(t, (<-holds).Held)
}

// A slot is held around one request, a turn admission for a whole run, so a
// run that escalates gives the slot back without giving up its admission and
// neither bound stands in for the other.
func TestSlotAndTurnAdmissionAreIndependent(t *testing.T) {
	t.Parallel()

	s := sched.New(sched.WithMemory(memory(12)), sched.WithLocalSlots(1))

	releaseTurn, err := s.AdmitTurn(t.Context(), "alpha")
	require.NoError(t, err)

	releaseSlot, err := s.AdmitSlot(t.Context(), "alpha")
	require.NoError(t, err)

	releaseSlot()

	// The run escalated: it still holds its turn admission, and another
	// thread may take the slot it gave back.
	second, err := s.AdmitSlot(t.Context(), "beta")
	require.NoError(t, err)

	second()
	releaseTurn()
}

// An unbounded scheduler is what a caller in front of a hosted tier wants,
// and it must not block on a slot it never had.
func TestSlotsUnboundedByDefault(t *testing.T) {
	t.Parallel()

	s := sched.New(sched.WithMemory(memory(12)))

	first, err := s.AdmitSlot(t.Context(), "alpha")
	require.NoError(t, err)

	second, err := s.AdmitSlot(t.Context(), "beta")
	require.NoError(t, err)

	second()
	first()
}

// The slot goes to whoever has waited longest. Waking every waiter to race
// for it left one of three threads with no turn at all for two minutes on
// this laptop's single-slot server.
func TestSlotsAreHandedOverInOrder(t *testing.T) {
	t.Parallel()

	s := sched.New(sched.WithMemory(memory(12)), sched.WithLocalSlots(1))
	holds := make(chan sched.Hold, 8)
	s.OnHold(func(h sched.Hold) { holds <- h })

	release, err := s.AdmitSlot(t.Context(), "alpha")
	require.NoError(t, err)

	order := make(chan string, 2)

	for _, name := range []string{"beta", "gamma"} {
		waiting := make(chan struct{})

		go func() {
			close(waiting)

			r, aErr := s.AdmitSlot(t.Context(), name)
			if aErr != nil {
				return
			}

			order <- name
			r()
		}()

		<-waiting
		// The hold event is only sent once the caller is queued, so beta is
		// ahead of gamma rather than racing it.
		require.True(t, (<-holds).Held)
	}

	release()

	assert.Equal(t, "beta", <-order)
	assert.Equal(t, "gamma", <-order)
	assert.Equal(t, 0, s.Snapshot(t.Context()).SlotsHeld)
}

// A caller that gives up while queued must not leave the slot held, and
// must not leave the queue holding a channel nothing will read.
func TestAbandonedSlotWaitDoesNotLeak(t *testing.T) {
	t.Parallel()

	s := sched.New(sched.WithMemory(memory(12)), sched.WithLocalSlots(1))
	holds := make(chan sched.Hold, 4)
	s.OnHold(func(h sched.Hold) { holds <- h })

	release, err := s.AdmitSlot(t.Context(), "alpha")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	gone := make(chan struct{})

	go func() {
		_, aErr := s.AdmitSlot(ctx, "beta")
		assert.Error(t, aErr)
		close(gone)
	}()

	require.True(t, (<-holds).Held)
	cancel()
	<-gone

	release()

	assert.Equal(t, 0, s.Snapshot(t.Context()).SlotsHeld)

	third, err := s.AdmitSlot(t.Context(), "gamma")
	require.NoError(t, err)
	third()
}
