package finish_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/finish"
)

// openedSet is what the run read, which is what Scope answers in production.
type openedSet map[string]bool

func (o openedSet) Read(abs string) bool { return o[abs] }

// The run this check exists for wrote nothing, so no gate and no other
// bound saw it at all.
func TestAnswerReadsWhatItNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	for _, name := range []string{"read.css", "unread.css"} {
		if err := os.WriteFile(filepath.Join(root, "web", name), []byte("a{}"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	opened := openedSet{filepath.Join(root, "web", "read.css"): true}

	tests := []struct {
		name    string
		answer  string
		want    string
		changed []string
	}{
		{
			name:   "a file the run never opened is a claim from somewhere else",
			answer: "the rule in web/unread.css is unused",
			want:   "web/unread.css",
		},
		{
			name:   "a file the run read is grounded",
			answer: "the rule in web/read.css is unused",
		},
		{
			name:   "a path that does not exist is the other check's finding",
			answer: "see web/gone.css",
		},
		{
			name:    "a run that changed something is checked by the gates",
			answer:  "the rule in web/unread.css is unused",
			changed: []string{"other.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := finish.AnswerReadsWhatItNames(root, tc.answer, tc.changed, opened)

			if tc.want == "" {
				if !got.OK() {
					t.Fatalf("report = %q, want no findings", got)
				}

				return
			}

			if len(got.Findings) != 1 || got.Findings[0].Detail != tc.want {
				t.Fatalf("report = %q, want one finding naming %s", got, tc.want)
			}
		})
	}
}

// Without a scope the check abstains rather than failing every run, the way
// the other checks treat a missing index.
func TestAnswerReadsWhatItNames_AbstainsWithoutAScope(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "web", "a.css"), []byte("a{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := finish.AnswerReadsWhatItNames(root, "see web/a.css", nil, nil); !got.OK() {
		t.Fatalf("report = %q, want no findings", got)
	}
}
