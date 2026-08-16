package edit_test

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/edit"
)

var update = flag.Bool("update", false, "update golden files")

func TestReplace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		old        string
		next       string
		wantSource string
		wantAdded  int
		wantRemove int
		wantStart  int
		wantEnd    int
	}{
		{
			name:       "exact single match",
			source:     "package foo\n\nfunc a() {}\n",
			old:        "func a() {}",
			next:       "func b() {}",
			wantSource: "package foo\n\nfunc b() {}\n",
			wantAdded:  1,
			wantRemove: 1,
			wantStart:  3,
			wantEnd:    3,
		},
		{
			name: "multi-line replacement line range accounting",
			source: "line1\n" +
				"line2\n" +
				"line3\n" +
				"line4\n",
			old:        "line2\nline3",
			next:       "replA\nreplB\nreplC",
			wantSource: "line1\nreplA\nreplB\nreplC\nline4\n",
			wantAdded:  3,
			wantRemove: 2,
			wantStart:  2,
			wantEnd:    4,
		},
		{
			name:       "deletion",
			source:     "keep1\nremove me\nkeep2\n",
			old:        "remove me\n",
			next:       "",
			wantSource: "keep1\nkeep2\n",
			wantAdded:  0,
			wantRemove: 2,
			wantStart:  2,
			wantEnd:    1,
		},
		{
			name: "fuzzy indentation recovery: source tabs, old_string spaces",
			source: "func foo() {\n" +
				"\tif x {\n" +
				"\t\taCount := a.Count()\n" +
				"\t\tbCount := b.Count()\n" +
				"\t}\n" +
				"}\n",
			old:  "    aCount := a.Count()\n    bCount := b.Count()",
			next: "    aTotal := a.Count()\n    bTotal := b.Count()",
			wantSource: "func foo() {\n" +
				"\tif x {\n" +
				"\t\taTotal := a.Count()\n" +
				"\t\tbTotal := b.Count()\n" +
				"\t}\n" +
				"}\n",
			wantAdded:  2,
			wantRemove: 2,
			wantStart:  3,
			wantEnd:    4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := edit.Replace(tt.source, tt.old, tt.next)
			if err != nil {
				t.Fatalf("Replace() error = %v", err)
			}

			checkResult(t, got, tt.wantSource, tt.wantAdded, tt.wantRemove, tt.wantStart, tt.wantEnd)
		})
	}
}

func checkResult(t *testing.T, got edit.Result, wantSource string, wantAdded, wantRemove, wantStart, wantEnd int) {
	t.Helper()

	if got.Source != wantSource {
		t.Errorf("Source = %q, want %q", got.Source, wantSource)
	}

	if got.Added != wantAdded {
		t.Errorf("Added = %d, want %d", got.Added, wantAdded)
	}

	if got.Removed != wantRemove {
		t.Errorf("Removed = %d, want %d", got.Removed, wantRemove)
	}

	if len(got.Ranges) != 1 {
		t.Fatalf("Ranges = %v, want exactly one", got.Ranges)
	}

	if got.Ranges[0].Start != wantStart || got.Ranges[0].End != wantEnd {
		t.Errorf("Ranges[0] = %+v, want {Start:%d End:%d}", got.Ranges[0], wantStart, wantEnd)
	}
}

func TestReplace_EmptyOldString(t *testing.T) {
	t.Parallel()

	_, err := edit.Replace("source", "", "new")
	if !errors.Is(err, edit.ErrEmptyOldString) {
		t.Errorf("err = %v, want ErrEmptyOldString", err)
	}
}

func TestReplace_NoChange(t *testing.T) {
	t.Parallel()

	_, err := edit.Replace("source", "same", "same")
	if !errors.Is(err, edit.ErrNoChange) {
		t.Errorf("err = %v, want ErrNoChange", err)
	}
}

func TestReplace_NotFound(t *testing.T) {
	t.Parallel()

	t.Run("no near candidate", func(t *testing.T) {
		t.Parallel()

		_, err := edit.Replace("apples and oranges\n", "bananas", "kiwis")

		notFound := requireNotFound(t, err)
		if notFound.CandidateText != "" {
			t.Errorf("CandidateText = %q, want empty", notFound.CandidateText)
		}
	})

	t.Run("with a near candidate", func(t *testing.T) {
		t.Parallel()

		source := "func foo() {\n\treturn 1\n\treturn 2\n}\n"

		_, err := edit.Replace(source, "    return 1\n    return 3", "    return 4\n    return 5")

		notFound := requireNotFound(t, err)
		if notFound.CandidateLine != 2 {
			t.Errorf("CandidateLine = %d, want 2", notFound.CandidateLine)
		}

		wantText := "\treturn 1\n\treturn 2"
		if notFound.CandidateText != wantText {
			t.Errorf("CandidateText = %q, want %q", notFound.CandidateText, wantText)
		}
	})
}

func TestReplace_NotUnique(t *testing.T) {
	t.Parallel()

	t.Run("exact matches report every line", func(t *testing.T) {
		t.Parallel()

		source := "dup()\nkeep\ndup()\nkeep\ndup()\n"

		_, err := edit.Replace(source, "dup()", "single()")

		checkNotUniqueLines(t, err, []int{1, 3, 5})
	})

	t.Run("fuzzy matches report every line", func(t *testing.T) {
		t.Parallel()

		source := "\tval := 1\nkeep\n\t\tval := 1\n"

		_, err := edit.Replace(source, "  val := 1", "  val := 2")

		checkNotUniqueLines(t, err, []int{1, 3})
	})
}

func requireNotFound(t *testing.T, err error) *edit.NotFoundError {
	t.Helper()

	if !errors.Is(err, edit.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	var notFound *edit.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}

	return notFound
}

func checkNotUniqueLines(t *testing.T, err error, want []int) {
	t.Helper()

	if !errors.Is(err, edit.ErrNotUnique) {
		t.Fatalf("err = %v, want ErrNotUnique", err)
	}

	var notUnique *edit.NotUniqueError
	if !errors.As(err, &notUnique) {
		t.Fatalf("err = %v, want *NotUniqueError", err)
	}

	if !slicesEqual(notUnique.Lines, want) {
		t.Errorf("Lines = %v, want %v", notUnique.Lines, want)
	}
}

func slicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestReplace_CRLFPreservation(t *testing.T) {
	t.Parallel()

	source := "package foo\r\n\r\nfunc a() {}\r\n"

	got, err := edit.Replace(source, "func a() {}", "func b() {}")
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	assertGolden(t, "testdata/crlf.golden", got.Source)
}

func assertGolden(t *testing.T, path, got string) {
	t.Helper()

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("writing golden file: %v", err)
		}
	}

	want, err := os.ReadFile(path) // #nosec G304 -- path is a fixed testdata literal
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}

	if string(want) != got {
		t.Errorf("output = %q, want %q (golden %s)", got, string(want), path)
	}
}

func TestNotFoundError_Error(t *testing.T) {
	t.Parallel()

	t.Run("without candidate", func(t *testing.T) {
		t.Parallel()

		err := &edit.NotFoundError{}
		if !errors.Is(err, edit.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("with candidate", func(t *testing.T) {
		t.Parallel()

		err := &edit.NotFoundError{CandidateLine: 5, CandidateText: "foo"}
		msg := err.Error()

		if !strings.Contains(msg, "line 5") || !strings.Contains(msg, "foo") {
			t.Errorf("Error() = %q, want it to mention line 5 and foo", msg)
		}
	})
}

// A long old_string used to echo a candidate as long as itself, so a bad
// anchor cost the model the file twice over.
func TestReplace_NotFoundCandidateIsBounded(t *testing.T) {
	t.Parallel()

	var source, old strings.Builder
	for i := range 200 {
		fmt.Fprintf(&source, "line %d\n", i)
		fmt.Fprintf(&old, "  line %d \n", i)
	}

	_, err := edit.Replace(source.String(), old.String(), "replacement\n")
	if err == nil {
		t.Fatal("expected a not-found error")
	}

	var nf *edit.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("got %T, want *edit.NotFoundError", err)
	}
	if got := strings.Count(nf.CandidateText, "\n") + 1; got > 12 {
		t.Fatalf("candidate is %d lines, want at most 12", got)
	}
	if nf.CandidateElided == 0 {
		t.Fatal("elided count not reported, so the model cannot tell it was truncated")
	}
	if !strings.Contains(err.Error(), "more lines not shown") {
		t.Fatalf("error message hides the truncation: %s", err.Error())
	}
}
