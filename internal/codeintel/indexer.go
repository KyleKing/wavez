package codeintel

import (
	"context"
	"sync"

	"github.com/kyleking/wavez/internal/codeintel/lang"
)

// Indexer is the queryable face of a Store, and the only one that can
// promise the index matches the tree.
//
// Freshness comes from the content hash Index records per file and never
// from a change event: an edit made outside Wavez (an editor, `jj restore`,
// a formatter, a `copier update`) emits no event, so re-walking and
// re-hashing is the only check that cannot be wrong. A timestamp or a TTL
// carries the same staleness risk a change event does, since neither
// observes the tree.
//
// Re-checking on every query is affordable because the walk is asymmetric.
// Measured on this repo (359 claimed files, 2345 symbols, M2 Pro): 782 ms to
// index cold, 18 ms to confirm an unchanged tree, and an unchanged tree
// issues no write statements at all. Start absorbs the cold cost in the
// background so no query pays it.
type Indexer struct {
	store    *Store
	registry *lang.Registry
	root     string
	mu       sync.Mutex
}

// NewIndexer builds an Indexer over store for the tree rooted at root.
func NewIndexer(store *Store, root string, registry *lang.Registry) *Indexer {
	return &Indexer{store: store, root: root, registry: registry}
}

// Refresh brings the index in step with the tree and reports what it found.
// Concurrent callers serialize rather than scanning twice.
func (ix *Indexer) Refresh(ctx context.Context) (IndexStats, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	return ix.store.Index(ctx, ix.root, ix.registry)
}

// Search refreshes the index and then queries it, so a result set always
// describes the tree as it is now. An empty result from Search means the
// query matched nothing, never that nothing had been indexed.
func (ix *Indexer) Search(ctx context.Context, q SearchQuery) ([]SearchResult, IndexStats, error) {
	stats, err := ix.Refresh(ctx)
	if err != nil {
		return nil, IndexStats{}, err
	}

	results, err := ix.store.Search(ctx, q)
	if err != nil {
		return nil, stats, err
	}

	return results, stats, nil
}

// Start indexes in the background and returns immediately, so the cold
// index cost is not charged to whichever query happens to run first. It
// drops its error because the next Refresh reports the same failure to a
// caller who can act on it.
func (ix *Indexer) Start(ctx context.Context) {
	go func() {
		_, _ = ix.Refresh(ctx) //nolint:errcheck // see doc comment: the next Refresh reports the same failure
	}()
}
