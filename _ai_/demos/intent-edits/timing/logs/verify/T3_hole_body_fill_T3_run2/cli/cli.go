// Package cli is a minimal stand-in for gh-repo-dashboard's internal/cli
// package, scoped to the one caller that renders models.NoteFile into the
// --cli JSON output.
package cli

import "t3notes/models"

// Repo is the stable JSON shape of one repo summary, mirroring the columns of
// the TUI's repo list view.
type Repo struct {
	Path       string            `json:"path"`
	NotesFiles []models.NoteFile `json:"notes_files,omitempty"`
}

// newRepo builds the JSON-facing Repo from a repo's detected notes files.
func newRepo(path string, notes []models.NoteFile) Repo {
	return Repo{
		Path:       path,
		NotesFiles: notes,
	}
}
