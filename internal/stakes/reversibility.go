package stakes

import (
	"path/filepath"
	"strings"
)

// Reversibility says whether undoing the touched paths is a cheap checkout
// of tracked files inside the project root, versus something that escapes
// it and a checkpoint cannot cleanly undo.
type Reversibility string

const (
	// Reversible means every touched path resolves inside the project root.
	Reversible Reversibility = "reversible"
	// Irreversible means at least one touched path resolves outside the
	// project root, or to the root itself.
	Irreversible Reversibility = "irreversible"
	// ReversibilityUnknown means no paths were supplied to check.
	ReversibilityUnknown Reversibility = "unknown"
)

// reversibilityOf reports whether every path in paths resolves inside root.
// An empty root or an empty paths list means the signal could not be
// computed.
func reversibilityOf(root string, paths []string) Reversibility {
	if root == "" || len(paths) == 0 {
		return ReversibilityUnknown
	}

	cleanRoot := filepath.Clean(root)
	for _, p := range paths {
		if !withinRoot(cleanRoot, p) {
			return Irreversible
		}
	}

	return Reversible
}

func withinRoot(root, path string) bool {
	if path == "" || path == "~" || path == "$HOME" || strings.HasPrefix(path, "~/") {
		return false
	}

	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(root, path))
	}

	if abs == root {
		return false
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
