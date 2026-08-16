package review

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kyleking/voice-cli/internal/corpus"
)

func candidates(n int) []corpus.Candidate {
	out := make([]corpus.Candidate, n)
	for i := range out {
		out[i] = corpus.Candidate{
			ID:      fmtID(i),
			Source:  "test",
			Author:  corpus.AuthorMe,
			Context: "unit test",
			Text:    fmtID(i),
		}
	}
	return out
}

func fmtID(i int) string {
	return "candidate-" + string(rune('a'+i))
}

func TestReview(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		candidate int
		wantLen   int
		wantText  string
		wantRed   bool
	}{
		{
			name:      "keep",
			input:     "k\n",
			candidate: 1,
			wantLen:   1,
			wantText:  "candidate-a",
			wantRed:   false,
		},
		{
			name:      "edit replaces text and marks redacted",
			input:     "e sanitized text\n",
			candidate: 1,
			wantLen:   1,
			wantText:  "sanitized text",
			wantRed:   true,
		},
		{
			name:      "skip drops the candidate",
			input:     "s\n",
			candidate: 1,
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			r := NewReviewer(strings.NewReader(tt.input), &out)

			kept, err := r.Review(candidates(tt.candidate))
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			if len(kept) != tt.wantLen {
				t.Fatalf("len(kept) = %d, want %d", len(kept), tt.wantLen)
			}
			if tt.wantLen == 0 {
				return
			}
			if kept[0].Text != tt.wantText {
				t.Errorf("Text = %q, want %q", kept[0].Text, tt.wantText)
			}
			if kept[0].Redacted != tt.wantRed {
				t.Errorf("Redacted = %v, want %v", kept[0].Redacted, tt.wantRed)
			}
		})
	}
}

func TestReviewQuitStopsEarlyButKeepsDecided(t *testing.T) {
	input := "k\nq\n"
	var out bytes.Buffer
	r := NewReviewer(strings.NewReader(input), &out)

	kept, err := r.Review(candidates(3))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(kept) != 1 {
		t.Fatalf("len(kept) = %d, want 1", len(kept))
	}
	if kept[0].ID != "candidate-a" {
		t.Errorf("kept[0].ID = %q, want candidate-a", kept[0].ID)
	}
}

func TestReviewMultipleDecisions(t *testing.T) {
	input := "k\ns\ne redacted\n"
	var out bytes.Buffer
	r := NewReviewer(strings.NewReader(input), &out)

	kept, err := r.Review(candidates(3))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("len(kept) = %d, want 2", len(kept))
	}
	if kept[0].ID != "candidate-a" || kept[0].Text != "candidate-a" {
		t.Errorf("kept[0] = %+v, want unedited candidate-a", kept[0])
	}
	if kept[1].ID != "candidate-c" || kept[1].Text != "redacted" || !kept[1].Redacted {
		t.Errorf("kept[1] = %+v, want redacted candidate-c", kept[1])
	}
}
