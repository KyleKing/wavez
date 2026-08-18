package lease_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/lease"
)

func TestSubtreeKeysOnTheWriteTargetsDirectory(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "repo")

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "nested file",
			target: filepath.Join(root, "internal", "vcs", "jj.go"),
			want:   filepath.Join("internal", "vcs"),
		},
		{name: "file at the root", target: filepath.Join(root, "main.go"), want: "."},
		{
			name:   "outside the root keys on itself",
			target: filepath.Join(string(filepath.Separator), "elsewhere", "x.go"),
			want:   filepath.Join(string(filepath.Separator), "elsewhere"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, lease.Subtree(root, tc.target))
		})
	}
}

func TestOverlapsCoversAncestorsAndDescendants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want bool
	}{
		{a: "internal/vcs", b: "internal/vcs", want: true},
		{a: "internal", b: "internal/vcs", want: true},
		{a: "internal/vcs", b: "internal", want: true},
		{a: ".", b: "internal/vcs", want: true},
		{a: "internal/vcs", b: "internal/api", want: false},
		{a: "internal/vc", b: "internal/vcs", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, lease.Overlaps(tc.a, tc.b))
		})
	}
}

// Two threads writing the same subtree serialize, a third writing elsewhere
// does not wait, and the wait names the holder: the M2 contention rule.
func TestAcquireSerializesOverlappingSubtrees(t *testing.T) {
	t.Parallel()

	waitCh := make(chan lease.Wait, 4)
	m := lease.New("/repo")
	m.OnWait(func(w lease.Wait) { waitCh <- w })

	first := lease.WithHolder(t.Context(), "alpha")
	second := lease.WithHolder(t.Context(), "beta")
	third := lease.WithHolder(t.Context(), "gamma")

	releaseAlpha, err := m.Acquire(first, "/repo/internal/vcs/jj.go")
	require.NoError(t, err)

	// A different subtree never waits, so this returns without alpha releasing.
	releaseGamma, err := m.Acquire(third, "/repo/internal/api/protocol.go")
	require.NoError(t, err)

	acquired := make(chan struct{})

	go func() {
		release, aErr := m.Acquire(second, "/repo/internal/vcs/git.go")
		if aErr == nil {
			release()
		}
		close(acquired)
	}()

	want := lease.Wait{Holder: "beta", Subtree: filepath.Join("internal", "vcs"), Blocker: "alpha", Waiting: true}
	assert.Equal(t, want, <-waitCh)

	assert.Equal(t, lease.Counts{Held: 2, Waiting: 1}, m.Counts())

	blocked := leaseFor(t, m, filepath.Join("internal", "vcs"))
	assert.Equal(t, lease.StateActive, blocked.State)
	assert.Equal(t, "alpha", blocked.Holder)
	assert.Equal(t, []string{"beta"}, blocked.Waiters)

	releaseAlpha()
	<-acquired
	releaseGamma()

	assert.Equal(t, lease.StateCommitted, leaseFor(t, m, filepath.Join("internal", "vcs")).State)

	assert.Zero(t, m.Counts().Waiting)
}

func TestAcquireIsReentrantForOneHolder(t *testing.T) {
	t.Parallel()

	m := lease.New("/repo")
	ctx := lease.WithHolder(t.Context(), "alpha")

	outer, err := m.Acquire(ctx, "/repo/internal/vcs/jj.go")
	require.NoError(t, err)

	inner, err := m.Acquire(ctx, "/repo/internal/vcs/git.go")
	require.NoError(t, err)

	inner()

	assert.Equal(t, lease.StateActive, leaseFor(t, m, filepath.Join("internal", "vcs")).State)

	outer()

	assert.Equal(t, lease.StateCommitted, leaseFor(t, m, filepath.Join("internal", "vcs")).State)
}

// A holder that stopped renewing must not block the tree forever, which is
// the one cleanup nothing else performs.
func TestExpiredLeaseStopsBlocking(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	m := lease.New("/repo", lease.WithTTL(time.Minute), lease.WithClock(clock))

	_, err := m.Acquire(lease.WithHolder(t.Context(), "alpha"), "/repo/internal/vcs/jj.go")
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)

	assert.Equal(t, lease.StateExpired, leaseFor(t, m, filepath.Join("internal", "vcs")).State)

	release, err := m.Acquire(lease.WithHolder(t.Context(), "beta"), "/repo/internal/vcs/git.go")
	require.NoError(t, err)
	release()
}

func TestAcquireWithoutAHolderFails(t *testing.T) {
	t.Parallel()

	_, err := lease.New("/repo").Acquire(t.Context(), "/repo/main.go")
	require.ErrorIs(t, err, lease.ErrNoHolder)
}

func TestAcquireOnANilManagerIsANoop(t *testing.T) {
	t.Parallel()

	var m *lease.Manager

	release, err := m.Acquire(context.Background(), "/repo/main.go")
	require.NoError(t, err)
	release()
	assert.Nil(t, m.List())
}

func leaseFor(t *testing.T, m *lease.Manager, subtree string) lease.Lease {
	t.Helper()

	for _, l := range m.List() {
		if l.Subtree == subtree {
			return l
		}
	}

	t.Fatalf("no lease on %s in %+v", subtree, m.List())

	return lease.Lease{}
}
