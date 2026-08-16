// Package codeintel owns the per-project SQLite code intelligence store:
// files, symbols, edges, full-text search, and coverage. It is fed
// incrementally by tree-sitter and queried through one Search entry point
// and one Context bundle, so every other subsystem reads one file instead
// of re-deriving this information.
package codeintel
