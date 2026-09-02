package daemon

import (
	"context"
	"testing"
	"time"
)

// Shutdown waits for connection handlers to return, and a handler blocked on
// a tool that will not answer never does: one wedged pty call kept a daemon
// alive through SIGTERM until it was killed outright. The wait is bounded, so
// the process leaves whether or not the handler ever comes back.
func TestWaitFor_ReturnsWhenTheWaitDoesNot(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		wait     func()
		deadline time.Duration
	}{
		"a wait that finishes": {
			wait:     func() {},
			deadline: time.Minute,
		},
		"a wait that never finishes": {
			wait:     func() { select {} },
			deadline: 50 * time.Millisecond,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), tt.deadline)
			defer cancel()

			done := make(chan struct{})

			go func() {
				defer close(done)
				waitFor(ctx, tt.wait)
			}()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("waitFor did not return")
			}
		})
	}
}
