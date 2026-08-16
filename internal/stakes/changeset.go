package stakes

import "sync"

// ChangeSet accumulates the edits one run has applied so far, so a later
// permission request can be scored against everything the run has already
// changed rather than against the pending action alone. An edit records
// only the replaced region, not the whole file, which is what
// capabilityDelta reads and what keeps a long run's memory bounded by the
// size of its edits.
//
// A ChangeSet never feeds Input.Paths: reversibility is a property of the
// action awaiting approval, and folding prior edits into it would report a
// command that escapes the project root as reversible.
type ChangeSet struct {
	edits []Edit
	mu    sync.Mutex
}

// NewChangeSet returns an empty ChangeSet, safe for concurrent use.
func NewChangeSet() *ChangeSet {
	return &ChangeSet{}
}

// Record appends one applied edit. A nil ChangeSet discards it, so a caller
// with no recorder wired does not need a branch.
func (c *ChangeSet) Record(e Edit) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.edits = append(c.edits, e)
}

// Edits returns a copy of every recorded edit, oldest first. A nil
// ChangeSet returns nil, which Compute reads as an unchecked signal rather
// than as an empty result.
func (c *ChangeSet) Edits() []Edit {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]Edit(nil), c.edits...)
}
