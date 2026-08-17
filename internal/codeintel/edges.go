package codeintel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// codegraphDBPath is where `codegraph init` writes its index, relative to
// the project root.
const codegraphDBPath = ".codegraph/codegraph.db"

// gitignorePerm is the mode ignoreCodegraphIndex creates a .gitignore
// with when the project has none.
const gitignorePerm = 0o600

// codegraphIgnoreEntry keeps the index InitAndRefresh creates out of the
// project's history.
const codegraphIgnoreEntry = ".codegraph/"

// defaultCodegraphBinary is the executable NewEdgeAdapter looks up on PATH.
const defaultCodegraphBinary = "codegraph"

// exactEdgeConfidence is the confidence recorded for a codegraph edge whose
// metadata carries none. Those edges come straight from the AST (a value
// reference, an interface implementation) rather than from a resolver that
// had to choose between candidates.
const exactEdgeConfidence = 1.0

// EdgeStats reports one edge refresh. Unavailable names why no graph was
// read, so an empty edges table with an empty Unavailable means the graph
// holds no edges rather than that none was ever built.
type EdgeStats struct {
	Unavailable string
	Read        int
	Copied      int
	Unresolved  int
	Reused      bool
}

// EdgeAdapter copies a codegraph index's call and reference edges into a
// store's edges table. The codegraph install is optional: a missing binary,
// a project that was never initialized, or a failing sync leaves the stored
// edges untouched and reports the reason through EdgeStats.Unavailable.
//
// InitAndRefresh is the one call that writes into the project it reads:
// it creates `.codegraph/` and adds that entry to the project's
// `.gitignore`. Refresh and Copy only read.
type EdgeAdapter struct {
	root         string
	binary       string
	initializing atomic.Bool
}

// EdgeAdapterOption configures an EdgeAdapter.
type EdgeAdapterOption func(*EdgeAdapter)

// WithCodegraphBinary overrides the executable name looked up on PATH.
func WithCodegraphBinary(name string) EdgeAdapterOption {
	return func(a *EdgeAdapter) { a.binary = name }
}

// NewEdgeAdapter builds an adapter over the codegraph index inside root.
func NewEdgeAdapter(root string, opts ...EdgeAdapterOption) *EdgeAdapter {
	adapter := &EdgeAdapter{root: root, binary: defaultCodegraphBinary}
	for _, opt := range opts {
		opt(adapter)
	}

	return adapter
}

// Refresh syncs the codegraph index and copies its edges into store,
// replacing whatever was there. It returns an error only when the store
// write fails: every codegraph failure comes back through
// EdgeStats.Unavailable so indexing survives without codegraph.
func (a *EdgeAdapter) Refresh(ctx context.Context, store *Store) (EdgeStats, error) {
	if err := a.sync(ctx); err != nil {
		//nolint:nilerr // see doc comment: codegraph failures are reported, not raised
		return EdgeStats{Unavailable: err.Error()}, nil
	}

	return a.Copy(ctx, store)
}

// InitAndRefresh builds the codegraph index when the project has none and
// then refreshes as Refresh does. It writes into the project being indexed:
// `codegraph init` creates `.codegraph/`, and the entry is appended to the
// project's `.gitignore` (created when absent) first, so the index is never
// left tracked. A project that already has an index is only refreshed, and
// its `.gitignore` is left alone.
//
// Init runs long enough to matter, so this belongs on a background path.
// A query wanting edges meanwhile should call Refresh, which reports the
// build through EdgeStats.Unavailable rather than waiting for it.
//
// Failures degrade exactly as Refresh's do: the reason comes back through
// EdgeStats.Unavailable with a nil error, and nothing records that init
// failed, so the next call tries again.
func (a *EdgeAdapter) InitAndRefresh(ctx context.Context, store *Store) (EdgeStats, error) {
	if err := a.init(ctx); err != nil {
		//nolint:nilerr // see doc comment: codegraph failures are reported, not raised
		return EdgeStats{Unavailable: err.Error()}, nil
	}

	return a.Refresh(ctx, store)
}

// Copy replaces store's edges with those of the codegraph index already on
// disk under the adapter's root. It never runs codegraph, so the edges are
// only as fresh as that index.
func (a *EdgeAdapter) Copy(ctx context.Context, store *Store) (EdgeStats, error) {
	raw, err := readCodegraphEdges(ctx, a.dbPath())
	if err != nil {
		//nolint:nilerr // see doc comment: codegraph failures are reported, not raised
		return EdgeStats{Unavailable: err.Error()}, nil
	}

	keys, err := store.symbolKeys(ctx)
	if err != nil {
		return EdgeStats{}, err
	}

	stats := EdgeStats{Read: len(raw)}
	edges := resolveEdges(raw, keys, &stats)
	if err := store.replaceEdges(ctx, edges); err != nil {
		return EdgeStats{}, err
	}
	stats.Copied = len(edges)

	return stats, nil
}

func (a *EdgeAdapter) dbPath() string {
	return filepath.Join(a.root, codegraphDBPath)
}

// buildingReason names the in-flight init when one is running, and is
// empty otherwise.
func (a *EdgeAdapter) buildingReason() string {
	if !a.initializing.Load() {
		return ""
	}

	return fmt.Sprintf("codegraph index at %s is being built", a.dbPath())
}

func (a *EdgeAdapter) init(ctx context.Context) error {
	if _, err := os.Stat(a.dbPath()); err == nil {
		return nil
	}

	binary, err := exec.LookPath(a.binary)
	if err != nil {
		return fmt.Errorf("locating %s: %w", a.binary, err)
	}

	a.initializing.Store(true)
	defer a.initializing.Store(false)

	if err := ignoreCodegraphIndex(a.root); err != nil {
		return err
	}

	//nolint:gosec // binary comes from LookPath on a configured name, root is the project root
	out, err := exec.CommandContext(ctx, binary, "init", a.root).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codegraph init: %w: %s", err, bytes.TrimSpace(out))
	}

	return nil
}

// ignoreCodegraphIndex appends codegraphIgnoreEntry to root's .gitignore
// unless an entry already covers it, creating the file when absent. The
// existing content is never rewritten or reordered.
func ignoreCodegraphIndex(root string) error {
	path := filepath.Join(root, ".gitignore")
	existing, err := os.ReadFile(path) //nolint:gosec // path is the project root's own .gitignore
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if ignoresCodegraph(string(existing)) {
		return nil
	}

	addition := codegraphIgnoreEntry + "\n"
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		addition = "\n" + addition
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, gitignorePerm) //nolint:gosec // as above
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	if _, err := file.WriteString(addition); err != nil {
		_ = file.Close() //nolint:errcheck // the write error is the one worth reporting

		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}

	return nil
}

func ignoresCodegraph(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case codegraphIgnoreEntry, strings.TrimSuffix(codegraphIgnoreEntry, "/"):
			return true
		}
	}

	return false
}

func (a *EdgeAdapter) sync(ctx context.Context) error {
	if _, err := os.Stat(a.dbPath()); err != nil {
		if reason := a.buildingReason(); reason != "" {
			return fmt.Errorf("%s: %w", reason, err)
		}

		return fmt.Errorf("no codegraph index at %s, run `codegraph init`: %w", a.dbPath(), err)
	}

	binary, err := exec.LookPath(a.binary)
	if err != nil {
		return fmt.Errorf("locating %s: %w", a.binary, err)
	}

	//nolint:gosec // binary comes from LookPath on a configured name, root is the project root
	out, err := exec.CommandContext(ctx, binary, "sync", "--quiet", a.root).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codegraph sync: %w: %s", err, bytes.TrimSpace(out))
	}

	return nil
}

// nodeRef locates a codegraph node the way this store identifies a symbol:
// by root-relative path, name, and 1-based declaration line. No byte offset
// is recorded on the codegraph side, so the line is what joins the two.
type nodeRef struct {
	path string
	name string
	line int
}

type rawEdge struct {
	kind     string
	metadata string
	src      nodeRef
	dst      nodeRef
}

func readCodegraphEdges(ctx context.Context, dbPath string) ([]rawEdge, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no codegraph index at %s, run `codegraph init`: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("opening codegraph index %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }() //nolint:errcheck // read-only handle over a foreign index

	// The `contains` and `imports` kinds are left behind: their endpoints
	// are file and import nodes, which have no counterpart in symbols.
	rows, err := db.QueryContext(ctx, `
		SELECT edges.kind, COALESCE(edges.metadata, ''),
		       src.file_path, src.name, src.start_line,
		       dst.file_path, dst.name, dst.start_line
		FROM edges
		JOIN nodes src ON src.id = edges.source
		JOIN nodes dst ON dst.id = edges.target
		WHERE edges.kind IN ('calls', 'extends', 'implements', 'instantiates', 'references')
		ORDER BY edges.id`)
	if err != nil {
		return nil, fmt.Errorf("reading codegraph edges from %s: %w", dbPath, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	var raw []rawEdge
	for rows.Next() {
		var e rawEdge
		if err := rows.Scan(&e.kind, &e.metadata,
			&e.src.path, &e.src.name, &e.src.line,
			&e.dst.path, &e.dst.name, &e.dst.line); err != nil {
			return nil, fmt.Errorf("scanning codegraph edge: %w", err)
		}
		raw = append(raw, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading codegraph edge rows: %w", err)
	}

	return raw, nil
}

// symbolKeys maps every indexed symbol's (path, name, line) to its
// edges.src/edges.dst key. Two symbols sharing all three would be
// indistinguishable to codegraph, so the lowest start byte wins and the
// mapping stays deterministic.
func (s *Store) symbolKeys(ctx context.Context) (map[nodeRef]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT files.path, symbols.name, symbols.start_line, symbols.start_byte
		FROM symbols JOIN files ON files.id = symbols.file_id
		ORDER BY files.path, symbols.start_byte`)
	if err != nil {
		return nil, fmt.Errorf("loading symbol keys: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	keys := make(map[nodeRef]string)
	for rows.Next() {
		var ref nodeRef
		var startByte uint
		if err := rows.Scan(&ref.path, &ref.name, &ref.line, &startByte); err != nil {
			return nil, fmt.Errorf("scanning symbol key: %w", err)
		}
		if _, seen := keys[ref]; seen {
			continue
		}
		keys[ref] = symbolKey(ref.path, ref.name, startByte)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading symbol key rows: %w", err)
	}

	return keys, nil
}

type edgeIdentity struct {
	src  string
	dst  string
	kind string
}

func resolveEdges(raw []rawEdge, keys map[nodeRef]string, stats *EdgeStats) []Edge {
	best := make(map[edgeIdentity]float64, len(raw))
	for _, e := range raw {
		src, srcOK := keys[e.src]
		dst, dstOK := keys[e.dst]
		if !srcOK || !dstOK {
			stats.Unresolved++

			continue
		}
		id := edgeIdentity{src: src, dst: dst, kind: e.kind}
		if confidence := edgeConfidence(e.metadata); confidence > best[id] {
			best[id] = confidence
		}
	}

	edges := make([]Edge, 0, len(best))
	for id, confidence := range best {
		edges = append(edges, Edge{Src: id.src, Dst: id.dst, Kind: id.kind, Confidence: confidence})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Src != edges[j].Src {
			return edges[i].Src < edges[j].Src
		}
		if edges[i].Dst != edges[j].Dst {
			return edges[i].Dst < edges[j].Dst
		}

		return edges[i].Kind < edges[j].Kind
	})

	return edges
}

func edgeConfidence(metadata string) float64 {
	var parsed struct {
		Confidence *float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil || parsed.Confidence == nil {
		return exactEdgeConfidence
	}

	return *parsed.Confidence
}

func (s *Store) replaceEdges(ctx context.Context, edges []Edge) error {
	return s.withWrite(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM edges`); err != nil {
			return fmt.Errorf("clearing edges: %w", err)
		}
		for _, e := range edges {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO edges (src, dst, kind, confidence) VALUES (?, ?, ?, ?)`,
				e.Src, e.Dst, e.Kind, e.Confidence); err != nil {
				return fmt.Errorf("inserting edge %s -> %s: %w", e.Src, e.Dst, err)
			}
		}

		return nil
	})
}
