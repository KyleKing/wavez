package gate_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// trackingGate records how many gates were mid-Run concurrently with it, so
// the test can assert on overlap rather than on timing.
type trackingGate struct {
	active    *int32Counter
	maxSeen   *int32Counter
	block     <-chan struct{}
	name      string
	resources []string
}

func (g *trackingGate) Name() string        { return g.name }
func (g *trackingGate) Resources() []string { return g.resources }

func (g *trackingGate) Run(_ context.Context, _ gate.RunContext) (gate.Result, error) {
	n := g.active.inc()
	g.maxSeen.max(n)
	<-g.block
	g.active.dec()

	return gate.Result{Gate: g.name, Pass: true}, nil
}

type int32Counter struct {
	mu  sync.Mutex
	val int
	hi  int
}

func (c *int32Counter) inc() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.val++

	return c.val
}

func (c *int32Counter) dec() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.val--
}

func (c *int32Counter) max(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if n > c.hi {
		c.hi = n
	}
}

func (c *int32Counter) high() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.hi
}

func TestRunGatesResourceScheduling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resourcesA  []string
		resourcesB  []string
		wantMaxSeen int
	}{
		{
			name:       "shared resource serializes",
			resourcesA: []string{"worktree"}, resourcesB: []string{"worktree"},
			wantMaxSeen: 1,
		},
		{
			name:       "independent resources run in parallel",
			resourcesA: []string{"go-test"}, resourcesB: []string{"worktree"},
			wantMaxSeen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			block := make(chan struct{})
			active := &int32Counter{}
			maxSeen := &int32Counter{}

			gates := []gate.Gate{
				&trackingGate{name: "a", resources: tt.resourcesA, active: active, maxSeen: maxSeen, block: block},
				&trackingGate{name: "b", resources: tt.resourcesB, active: active, maxSeen: maxSeen, block: block},
			}

			done := make(chan []gate.Result, 1)
			go func() {
				done <- gate.RunGates(context.Background(), gate.RealClock{}, gates, gate.RunContext{})
			}()

			waitForBothRunning(t, active, tt.wantMaxSeen)
			close(block)

			results := <-done
			if len(results) != 2 {
				t.Fatalf("got %d results, want 2", len(results))
			}
			if got := maxSeen.high(); got != tt.wantMaxSeen {
				t.Errorf("max concurrent gates = %d, want %d", got, tt.wantMaxSeen)
			}
		})
	}
}

func waitForBothRunning(t *testing.T, c *int32Counter, n int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		v := c.val
		c.mu.Unlock()

		if v >= n {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("timed out waiting for %d gates to be running concurrently", n)
}
