package codeintel

import (
	"testing"
	"time"
)

// The first pass holds the walk lock for as long as the tree takes, which on
// a 244 MB checkout is 14.5 seconds. Holding mu here stands in for that: a
// Refresh that took the lock would deadlock this rather than fail it.
func TestIndexerRefresh_DoesNotWaitOutTheFirstPass(t *testing.T) {
	t.Parallel()

	ix := &Indexer{}
	ix.building.Store(true)
	ix.mu.Lock()
	defer ix.mu.Unlock()

	stats, err := ix.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh during the first pass: %v", err)
	}

	if stats.Building == "" {
		t.Fatal("Building is empty, want a query during the first pass told the answer is partial")
	}
}

// The bypass is the flag and nothing else, so a Refresh after the first pass
// serializes with whatever holds the walk.
func TestIndexerRefresh_WaitsOnceTheFirstPassIsDone(t *testing.T) {
	t.Parallel()

	ix := &Indexer{}
	ix.mu.Lock()

	returned := make(chan struct{})
	go func() {
		defer close(returned)

		_, _ = ix.Refresh(t.Context()) //nolint:errcheck // the call is expected to block, not to answer
	}()

	select {
	case <-returned:
		t.Fatal("Refresh answered while the walk lock was held, so the flag is not what bypasses it")
	case <-time.After(100 * time.Millisecond):
	}

	ix.mu.Unlock()
	<-returned
}
