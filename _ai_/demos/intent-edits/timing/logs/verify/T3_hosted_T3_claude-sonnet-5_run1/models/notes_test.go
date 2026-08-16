package models_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"t3notes/models"
)

func TestDetectNotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  []models.NoteFile
	}{
		{name: "no notes file", files: nil, want: nil},
		{
			name:  "doing.md only",
			files: map[string]string{"doing.md": "Working on M8\nmore detail\n"},
			want:  []models.NoteFile{{Name: "doing.md", FirstLine: "Working on M8", SizeBytes: 26}},
		},
		{
			name:  "TODO.md only",
			files: map[string]string{"TODO.md": "Fix the bug\n"},
			want:  []models.NoteFile{{Name: "TODO.md", FirstLine: "Fix the bug", SizeBytes: 12}},
		},
		{
			name: "multiple matches are all returned, in configured order",
			files: map[string]string{
				".doing":   "from dotfile",
				"doing.md": "from markdown",
			},
			want: []models.NoteFile{
				{Name: ".doing", FirstLine: "from dotfile", SizeBytes: 12},
				{Name: "doing.md", FirstLine: "from markdown", SizeBytes: 13},
			},
		},
		{
			name: "doing.md and doing.txt are both returned",
			files: map[string]string{
				"doing.md":  "from markdown",
				"doing.txt": "from txt",
			},
			want: []models.NoteFile{
				{Name: "doing.md", FirstLine: "from markdown", SizeBytes: 13},
				{Name: "doing.txt", FirstLine: "from txt", SizeBytes: 8},
			},
		},
		{
			name:  "skips leading blank lines",
			files: map[string]string{"doing.md": "\n\n  \nActual first line\nsecond\n"},
			want:  []models.NoteFile{{Name: "doing.md", FirstLine: "Actual first line", SizeBytes: 30}},
		},
		{
			name:  "empty file yields empty first line",
			files: map[string]string{"doing.md": ""},
			want:  []models.NoteFile{{Name: "doing.md", FirstLine: ""}},
		},
		{
			name:  "size bytes reflects file length on disk",
			files: map[string]string{"doing.md": "abcde"},
			want:  []models.NoteFile{{Name: "doing.md", FirstLine: "abcde", SizeBytes: 5}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatalf("writing fixture file: %v", err)
				}
			}

			got := models.DetectNotes(dir)
			if !slices.Equal(got, tt.want) {
				t.Errorf("DetectNotes() = %+v; want %+v", got, tt.want)
			}
		})
	}
}

func TestReadNotesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "line one\nline two\n"
	if err := os.WriteFile(filepath.Join(dir, "doing.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	tests := []struct {
		name      string
		notesFile string
		want      string
	}{
		{name: "no notes file", notesFile: "", want: ""},
		{name: "missing file", notesFile: "doing.txt", want: ""},
		{name: "existing file", notesFile: "doing.md", want: content},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := models.ReadNotesFile(dir, tt.notesFile); got != tt.want {
				t.Errorf("ReadNotesFile() = %q; want %q", got, tt.want)
			}
		})
	}
}
