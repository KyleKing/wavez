package models

import (
	"os"
	"path/filepath"
	"strings"
)

// notesFilenames lists candidate per-repo notes files, checked in this order.
var notesFilenames = []string{".doing", "doing.md", "doing.txt", "TODO.md"}

// SetNotesFilenames replaces the candidate notes filename list. Intended for
// startup config application only; not safe to call concurrently with
// DetectNotes.
func SetNotesFilenames(names []string) {
	if len(names) > 0 {
		notesFilenames = names
	}
}

const notesContentReadLimit = 64 * 1024

// NoteFile identifies one notes file found at a repo's root, along with its
// first non-empty line for a quick preview without reading the full content,
// and its size in bytes on disk.
type NoteFile struct {
	Name      string `json:"name"`
	FirstLine string `json:"first_line,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
}

// DetectNotes finds every candidate notes file present at repoPath's root and
// returns one NoteFile per match, in notesFilenames order. A repo with none
// returns nil; a read failure yields an empty first line, not an error, since
// notes detection is best-effort.
func DetectNotes(repoPath string) []NoteFile {
	var found []NoteFile

	for _, name := range notesFilenames {
		path := filepath.Join(repoPath, name)

		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}

		found = append(found, NoteFile{Name: name, FirstLine: firstNonEmptyLine(readCapped(path)), SizeBytes: info.Size()})
	}

	return found
}

// ReadNotesFile reads the full content of a notes file previously identified
// by DetectNotes, capped at a modest size. A read failure yields "".
func ReadNotesFile(repoPath, notesFile string) string {
	if notesFile == "" {
		return ""
	}

	return readCapped(filepath.Join(repoPath, notesFile))
}

// NoteFileContent pairs a notes file's name with its full content.
type NoteFileContent struct {
	Name    string
	Content string
}

// ReadNotesFiles reads the full content of each file DetectNotes found, in
// the same order.
func ReadNotesFiles(repoPath string, files []NoteFile) []NoteFileContent {
	contents := make([]NoteFileContent, len(files))
	for i, f := range files {
		contents[i] = NoteFileContent{Name: f.Name, Content: ReadNotesFile(repoPath, f.Name)}
	}

	return contents
}

func readCapped(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // repo root + fixed filename
	if err != nil {
		return ""
	}

	if len(data) > notesContentReadLimit {
		data = data[:notesContentReadLimit]
	}

	return string(data)
}

func firstNonEmptyLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
