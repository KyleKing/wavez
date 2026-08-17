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
	store     *Store
	registry  *lang.Registry
	edges     *EdgeAdapter
	root      string
	lastEdges EdgeStats
	copied    bool
	mu        sync.Mutex
	// edgesMu is held across `codegraph init`, so it must never be taken
	// with mu or a build would block every query the tree scan serves.
	edgesMu sync.Mutex
}

// IndexerOption configures an Indexer.
type IndexerOption func(*Indexer)

// WithEdgeAdapter replaces the codegraph adapter an Indexer refreshes edges
// through.
func WithEdgeAdapter(adapter *EdgeAdapter) IndexerOption {
	return func(ix *Indexer) { ix.edges = adapter }
}

// NewIndexer builds an Indexer over store for the tree rooted at root.
func NewIndexer(store *Store, root string, registry *lang.Registry, opts ...IndexerOption) *Indexer {
	ix := &Indexer{store: store, root: root, registry: registry, edges: NewEdgeAdapter(root)}
	for _, opt := range opts {
		opt(ix)
	}

	return ix
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
//
// A graph query refreshes the edge copy too, since edges no query has
// recopied are stale in exactly the way the freshness doctrine exists to
// prevent. The content-hash gate makes that free on an unchanged tree.
func (ix *Indexer) Search(ctx context.Context, q SearchQuery) ([]SearchResult, IndexStats, error) {
	refresh := func(ctx context.Context) (IndexStats, error) { return ix.Refresh(ctx) }
	if q.Mode == SearchGraph {
		refresh = func(ctx context.Context) (IndexStats, error) {
			edges, err := ix.RefreshEdges(ctx)
			if err != nil {
				return IndexStats{}, err
			}

			stats, err := ix.Refresh(ctx)
			stats.EdgesUnavailable = edges.Unavailable

			return stats, err
		}
	}

	stats, err := refresh(ctx)
	if err != nil {
		return nil, IndexStats{}, err
	}

	results, err := ix.store.Search(ctx, q)
	if err != nil {
		return nil, stats, err
	}

	return results, stats, nil
}

// RefreshEdges refreshes the index and then recopies the codegraph graph if
// the tree moved under it. Copying is its own call rather than part of
// Refresh because it shells out to another process, which no query should
// wait on unless it wants edges.
//
// The same hash the file index uses gates the copy: a Refresh that wrote no
// file and removed none cannot have changed the graph, so the previous
// EdgeStats comes back with Reused set. A copy that never succeeded leaves
// the gate open, so installing or initializing codegraph mid-session takes
// effect on the next call instead of waiting for an edit.
//
// It never builds a missing index, since that writes into the project and
// takes long enough that no query should wait on it. While InitEdges is
// building one, this reports that through EdgeStats.Unavailable instead of
// blocking behind it.
func (ix *Indexer) RefreshEdges(ctx context.Context) (EdgeStats, error) {
	if reason := ix.edges.buildingReason(); reason != "" {
		return EdgeStats{Unavailable: reason}, nil
	}

	return ix.refreshAndCopy(ctx, ix.edges.Refresh)
}

// InitEdges is RefreshEdges plus the one call that writes into the project:
// it builds the codegraph index when there is none and adds `.codegraph/`
// to the project's .gitignore. Start is where it belongs, because a query
// cannot afford the build.
func (ix *Indexer) InitEdges(ctx context.Context) (EdgeStats, error) {
	return ix.refreshAndCopy(ctx, ix.edges.InitAndRefresh)
}

func (ix *Indexer) refreshAndCopy(
	ctx context.Context,
	copyEdges func(context.Context, *Store) (EdgeStats, error),
) (EdgeStats, error) {
	stats, err := ix.Refresh(ctx)
	if err != nil {
		return EdgeStats{}, err
	}

	ix.edgesMu.Lock()
	defer ix.edgesMu.Unlock()

	if ix.copied && stats.FilesIndexed == 0 && stats.FilesRemoved == 0 {
		reused := ix.lastEdges
		reused.Reused = true

		return reused, nil
	}

	edgeStats, err := copyEdges(ctx, ix.store)
	if err != nil {
		return EdgeStats{}, err
	}
	ix.lastEdges = edgeStats
	ix.copied = edgeStats.Unavailable == ""

	return edgeStats, nil
}

// Start indexes and copies edges in the background and returns immediately,
// so neither cold cost is charged to whichever query happens to run first.
// This is the path that builds a missing codegraph index, so starting an
// Indexer over a project that has none writes `.codegraph/` and a
// `.gitignore` entry for it.
//
// It drops its error because the next Refresh reports the same failure to a
// caller who can act on it.
func (ix *Indexer) Start(ctx context.Context) {
	go func() {
		_, _ = ix.InitEdges(ctx) //nolint:errcheck // see doc comment: the next Refresh reports the same failure
	}()
}
