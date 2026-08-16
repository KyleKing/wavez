// Package palette is a trimmed copy of gh-repo-dashboard's
// internal/app/palette.go, adapted for the edit-loop spike. A findTag kind
// has been added to the enum but the switch in kindName has not been
// updated to handle it yet.
package palette

const nameBranch = "branch"

// findKind is the object type a palette result belongs to.
type findKind int

const (
	findAny findKind = iota
	findRepo
	findBranch
	findPR
	findStash
	findNote
	findTag
)

type findResult struct {
	kind findKind
}

func (r *findResult) kindName() string {
	switch r.kind {
	case findRepo:
		return "repo"
	case findBranch:
		return nameBranch
	case findPR:
		return "PR"
	case findStash:
		return "stash"
	case findNote:
		return "note"
	case findAny:
		return ""
	}

	return ""
}
