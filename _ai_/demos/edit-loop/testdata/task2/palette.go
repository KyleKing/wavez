// Package palette is a trimmed copy of gh-repo-dashboard's
// internal/app/palette.go, adapted for the edit-loop spike. The guard clause
// in homogeneousKind that the real file has has been removed on purpose: the
// version here panics on an empty slice.
package palette

// findKind is the object type a palette result belongs to.
type findKind int

const (
	findAny findKind = iota
	findRepo
	findBranch
	findPR
)

type findResult struct {
	kind findKind
	repo string
}

// homogeneousKind returns the one kind every result shares, or findAny when
// the set is mixed or empty.
func homogeneousKind(results []findResult) findKind {
	kind := results[0].kind
	for _, r := range results[1:] {
		if r.kind != kind {
			return findAny
		}
	}

	return kind
}
