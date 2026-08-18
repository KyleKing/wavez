package routine

import "testing"

// TestGroupNext_RotationPolicy pins the admission order directly, because
// racing three real runs onto one key can only observe the outcome of the
// policy and never the queue it chose from.
func TestGroupNext_RotationPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		lastServed string
		queued     []string
		rotate     bool
		want       int
	}{
		{name: "empty queue", want: -1},
		{name: "queue is first in first out", queued: []string{"a", "a", "b"}, lastServed: "a", want: 0},
		{
			name:   "round robin skips the routine just served",
			queued: []string{"a", "b"}, lastServed: "a", rotate: true, want: 1,
		},
		{
			name:   "round robin falls back to the head when every waiter is the same routine",
			queued: []string{"a", "a"}, lastServed: "a", rotate: true, want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := &group{lastServed: tc.lastServed, rotate: tc.rotate}
			for _, name := range tc.queued {
				g.waiters = append(g.waiters, &waiter{routine: name, ready: make(chan struct{})})
			}

			if got := g.next(); got != tc.want {
				t.Errorf("next() = %d, want %d", got, tc.want)
			}
		})
	}
}
