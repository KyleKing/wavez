package edit_test

import (
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/edit"
)

func assertNoSpansIsANoop(t *testing.T, src string) {
	t.Helper()

	again, _, err := edit.ApplySpans(src, nil)
	if err != nil {
		t.Errorf("applying no spans failed: %v", err)
	}

	if again != src {
		t.Errorf("applying no spans changed the source")
	}
}

func TestApplySpans(t *testing.T) {
	t.Parallel()

	const src = "package a\n\nfunc Alpha() string { return Alpha2() }\n"

	tests := []struct {
		wantErr error
		name    string
		src     string
		want    string
		spans   []edit.Span
	}{
		{
			name: "two spans on one line apply without shifting each other",
			src:  src,
			spans: []edit.Span{
				{Line: 2, Column: 5, EndLine: 2, EndColumn: 10, NewText: "Beta"},
				{Line: 2, Column: 29, EndLine: 2, EndColumn: 35, NewText: "Gamma"},
			},
			want: "package a\n\nfunc Beta() string { return Gamma() }\n",
		},
		{
			// A rename lands on the same identifier in several files and, in
			// one of them, several times on one line. Applying in file order
			// would move every later span by the length delta of the first.
			name: "a span after a longer replacement still lands right",
			src:  "aaa bbb aaa\n",
			spans: []edit.Span{
				{Line: 0, Column: 0, EndLine: 0, EndColumn: 3, NewText: "wwwwwwww"},
				{Line: 0, Column: 8, EndLine: 0, EndColumn: 11, NewText: "z"},
			},
			want: "wwwwwwww bbb z\n",
		},
		{
			// A column counted in UTF-16 code units, which is what the
			// protocol states and what a byte or rune count would get wrong.
			name: "a column past an astral character counts utf-16 units",
			src:  "x := \"\U0001F600\" // aaa\n",
			spans: []edit.Span{
				{Line: 0, Column: 13, EndLine: 0, EndColumn: 16, NewText: "bbb"},
			},
			want: "x := \"\U0001F600\" // bbb\n",
		},
		{
			name: "overlapping spans are refused rather than resolved",
			src:  "aaa bbb\n",
			spans: []edit.Span{
				{Line: 0, Column: 0, EndLine: 0, EndColumn: 5, NewText: "x"},
				{Line: 0, Column: 4, EndLine: 0, EndColumn: 7, NewText: "y"},
			},
			wantErr: edit.ErrSpanOutOfRange,
		},
		{
			name:    "a line the file does not have is refused",
			src:     "aaa\n",
			spans:   []edit.Span{{Line: 9, Column: 0, EndLine: 9, EndColumn: 1, NewText: "x"}},
			wantErr: edit.ErrSpanOutOfRange,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := edit.ApplySpans(tc.src, tc.spans)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("ApplySpans: %v", err)
			}

			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}

			assertNoSpansIsANoop(t, got)
		})
	}
}

// A span that removes the last declaration of a file ends one past the last
// line, which is the end of the source rather than a line that exists.
func TestApplySpansCanEndPastTheLastLine(t *testing.T) {
	t.Parallel()

	const src = "package a\n\nfunc Alpha() {}\n"

	got, _, err := edit.ApplySpans(src, []edit.Span{{Line: 2, Column: 0, EndLine: 3, EndColumn: 0}})
	if err != nil {
		t.Fatalf("ApplySpans: %v", err)
	}

	if got != "package a\n\n" {
		t.Errorf("got %q, want %q", got, "package a\n\n")
	}
}
