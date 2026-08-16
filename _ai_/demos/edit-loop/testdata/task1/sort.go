// Package sortutil is a trimmed, dependency-free copy of gh-repo-dashboard's
// internal/filters/sort.go, adapted for the edit-loop spike.
package sortutil

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RepoSummary is a minimal stand-in for models.RepoSummary.
type RepoSummary struct {
	Path         string
	LastModified time.Time
	Branch       string
	Dirty        bool
	Uncommitted  int
}

func (r *RepoSummary) IsDirty() bool         { return r.Dirty }
func (r *RepoSummary) UncommittedCount() int { return r.Uncommitted }

// SortPaths returns a copy of paths sorted by name, most-recently-modified first.
func SortPaths(paths []string, summaries map[string]RepoSummary) []string {
	if len(paths) == 0 {
		return paths
	}

	sorted := make([]string, len(paths))
	copy(sorted, paths)

	sort.Slice(sorted, func(i, j int) bool {
		si := summaries[sorted[i]]
		sj := summaries[sorted[j]]

		return compareByStatus(&si, &sj)
	})

	return sorted
}

func compareByName(a, b *RepoSummary) bool {
	return strings.ToLower(filepath.Base(a.Path)) < strings.ToLower(filepath.Base(b.Path))
}

// compareByStatus reports whether a should sort before b: dirty repos first,
// then by number of uncommitted changes, then by name.
func compareByStatus(a, b *RepoSummary) bool {
	aDirty := a.IsDirty()
	bDirty := b.IsDirty()

	if aDirty != bDirty {
		return aDirty
	}

	aCount := a.UncommittedCount()
	bCount := b.UncommittedCount()
	if aCount != bCount {
		return aCount > bCount
	}

	return compareByName(a, b)
}
