package gate_test

import (
	"context"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

func TestRunnerDebounceCoalescesABurst(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))

	var gotBatches [][]tool.Change

	runner := gate.NewRunner(clock, 50*time.Millisecond, func(_ context.Context, changes []tool.Change) gate.RunResult {
		gotBatches = append(gotBatches, changes)

		return gate.RunResult{Changes: changes}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runner.Start(ctx)

	runner.Enqueue(tool.Change{Path: "a.go", Added: 1})
	runner.Enqueue(tool.Change{Path: "b.go", Added: 2})
	runner.Enqueue(tool.Change{Path: "a.go", Added: 3})

	clock.Advance(50 * time.Millisecond)

	result := <-runner.Results()

	if len(result.Changes) != 2 {
		t.Fatalf("got %d coalesced changes, want 2: %+v", len(result.Changes), result.Changes)
	}
	if result.Changes[0].Path != "a.go" || result.Changes[0].Added != 4 {
		t.Errorf("a.go change = %+v, want Path=a.go Added=4 (1+3 coalesced)", result.Changes[0])
	}
	if result.Changes[1].Path != "b.go" || result.Changes[1].Added != 2 {
		t.Errorf("b.go change = %+v, want Path=b.go Added=2", result.Changes[1])
	}
	if len(gotBatches) != 1 {
		t.Fatalf("run invoked %d times, want exactly 1 for the whole burst", len(gotBatches))
	}
}

func TestRunnerDebounceResetsOnEachChange(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Unix(0, 0))

	runs := make(chan struct{}, 10)

	runner := gate.NewRunner(clock, 50*time.Millisecond, func(_ context.Context, changes []tool.Change) gate.RunResult {
		runs <- struct{}{}

		return gate.RunResult{Changes: changes}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runner.Start(ctx)

	runner.Enqueue(tool.Change{Path: "a.go"})
	clock.Advance(30 * time.Millisecond) // short of the debounce window: no run yet
	runner.Enqueue(tool.Change{Path: "a.go"})
	clock.Advance(30 * time.Millisecond) // still short, since the window reset on the second change

	select {
	case <-runs:
		t.Fatal("run fired before the debounce window elapsed since the last change")
	default:
	}

	clock.Advance(20 * time.Millisecond) // now 50ms past the second change
	<-runner.Results()

	if len(runs) != 1 {
		t.Fatalf("run fired %d times, want exactly 1", len(runs))
	}
}
