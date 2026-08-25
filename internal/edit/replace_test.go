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

// Half of every near-match report this project logged faced a blank source
// line, which is an anchor copied without the file's blank lines. The
// report for it read "source has: " with nothing after it, so the run had
// no way to see what it had dropped.
func TestReplace_MatchesAnAnchorMissingTheFilesBlankLines(t *testing.T) {
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
			name:       "an anchor without the file's blank lines",
			source:     "func f() {\n\ta()\n\n\tb()\n}\n",
			old:        "func f() {\n\ta()\n\tb()\n}",
			next:       "func f() {\n\tc()\n}",
			wantSource: "func f() {\n\tc()\n}\n",
			wantAdded:  3,
			wantRemove: 5,
			wantStart:  1,
			wantEnd:    3,
		},
		{
			name:       "an anchor without the file's blank lines or its indentation",
			source:     "func outer() {\n\tif x {\n\t\ta()\n\n\t\tb()\n\t}\n}\n",
			old:        "if x {\na()\nb()\n}",
			next:       "if y {\n\tz()\n}",
			wantSource: "func outer() {\n\tif y {\n\t\tz()\n\t}\n}\n",
			wantAdded:  3,
			wantRemove: 5,
			wantStart:  2,
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
		if notFound.CandidateLine != 0 {
			t.Errorf("CandidateLine = %d, want 0: nothing in the source is close", notFound.CandidateLine)
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

		if notFound.MismatchLine != 3 {
			t.Errorf("MismatchLine = %d, want 3: line 2 of the anchor is the wrong one", notFound.MismatchLine)
		}
		if strings.TrimSpace(notFound.Sent) != "return 3" || strings.TrimSpace(notFound.Found) != "return 2" {
			t.Errorf("Sent/Found = %q/%q, want the two lines that part", notFound.Sent, notFound.Found)
		}
	})

	// A source that gained one line the anchor does not have scores higher
	// aligned one line down, and the report then blames the anchor's first
	// line, which is the one line that was right. One replay lane read that
	// and re-sent the same anchor five times.
	t.Run("with a line the anchor does not have", func(t *testing.T) {
		t.Parallel()

		source := "func f() int {\n\t// what it does\n\tif x == 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}\n"
		anchor := "func f() int {\n\tif x == 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}"

		_, err := edit.Replace(source, anchor, "func f() int { return 2 }")

		notFound := requireNotFound(t, err)
		if notFound.CandidateLine != 1 {
			t.Errorf("CandidateLine = %d, want 1: the anchor starts exactly where it says", notFound.CandidateLine)
		}

		if notFound.MismatchLine != 2 {
			t.Errorf("MismatchLine = %d, want 2", notFound.MismatchLine)
		}

		if strings.TrimSpace(notFound.Found) != "// what it does" {
			t.Errorf("Found = %q, want the line the source has and the anchor omits", notFound.Found)
		}
	})
}

// An anchor whose only fault is trailing junk matches no line at all, so the
// line-by-line report has nothing to say. A run that closed a JSON string
// with a typographic quote hit exactly that and re-sent the same anchor
// until it died.
func TestReplace_NotFoundReportsWhereAOneLineAnchorParts(t *testing.T) {
	t.Parallel()

	source := "package daemon\n\nfunc firstDir(dirs []string) string {\n\treturn dirs[0]\n}\n"

	_, err := edit.Replace(source, "func firstDir(dirs []string) string {\u201d, ", "x")

	notFound := requireNotFound(t, err)
	if notFound.MismatchLine != 3 {
		t.Errorf("MismatchLine = %d, want 3", notFound.MismatchLine)
	}
	if !strings.Contains(notFound.Sent, "\u201d") {
		t.Errorf("Sent = %q, want the trailing junk the anchor added", notFound.Sent)
	}
	if notFound.Found != "(end of line)" {
		t.Errorf("Found = %q, want the end of the source line", notFound.Found)
	}
}

// Below a real prefix match the report would point at a coincidence.
// A report whose source line is blank printed "source has: " and stopped,
// which is what half of every near-match report this project logged looked
// like.
func TestNotFoundError_NamesABlankSourceLine(t *testing.T) {
	t.Parallel()

	err := (&edit.NotFoundError{CandidateLine: 1, MismatchLine: 3, Sent: "\treturn x", Found: ""}).Error()
	if !strings.Contains(err, "source has: a blank line") {
		t.Errorf("Error() = %q, want it to name the blank source line", err)
	}
}

func TestReplace_NotFoundStaysSilentWithNothingToSay(t *testing.T) {
	t.Parallel()

	_, err := edit.Replace("apples and oranges\n", "kiwi", "pear")

	if got := requireNotFound(t, err); got.CandidateLine != 0 {
		t.Errorf("CandidateLine = %d, want 0", got.CandidateLine)
	}
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

		err := &edit.NotFoundError{CandidateLine: 5, MismatchLine: 7, Sent: "foo", Found: "bar"}
		msg := err.Error()

		for _, want := range []string{"line 5", "line 7", "foo", "bar"} {
			if !strings.Contains(msg, want) {
				t.Errorf("Error() = %q, want it to mention %s", msg, want)
			}
		}
	})
}

// A long old_string must not echo a line as long as itself back, which on a
// generated or minified file would cost the model the file twice over.
func TestReplace_NotFoundReportIsBounded(t *testing.T) {
	t.Parallel()

	var source, old strings.Builder
	for i := range 200 {
		fmt.Fprintf(&source, "line %d %s\n", i, strings.Repeat("x", 400))
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

	for _, line := range strings.Split(err.Error(), "\n") {
		if len(line) > 300 {
			t.Fatalf("the report echoes a %d-character line: %s", len(line), line[:80])
		}
	}
}

// gofmt aligns struct fields, and the format gate runs it over every file
// the moment an edit lands. An anchor copied from what the model itself
// wrote then faces a line the harness re-spaced behind it, which is four of
// the near-match reports in this project's corpus and every one of them a
// table entry.
func TestReplaceMatchesAcrossFormatterSpacing(t *testing.T) {
	t.Parallel()

	source := "package a\n\nvar table = []entry{\n" +
		"\t{name:  \"first\", want:  1},\n\t{name:  \"second\", want: 22},\n}\n"

	got, err := edit.Replace(source,
		"\t{name: \"second\", want: 22},",
		"\t{name: \"second\", want: 33},")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if !strings.Contains(got.Source, "want: 33") {
		t.Errorf("Source = %q, want the replacement applied", got.Source)
	}
	if !strings.Contains(got.Source, "{name:  \"first\", want:  1}") {
		t.Errorf("Source = %q, want the untouched row left alone", got.Source)
	}
}

// A rename touches the same call written identically at several sites, and
// no widening tells them apart. One replay lane died stagnant sending the
// same pair five times against "widen old_string to make it unique".
func TestReplaceAllTakesEveryOccurrence(t *testing.T) {
	t.Parallel()

	source := "package a\n\nfunc one() { bench.Read(path) }\n\nfunc two() { bench.Read(path) }\n"

	if _, err := edit.Replace(source, "bench.Read(path)", "bench.ReadLog(path)"); !errors.Is(err, edit.ErrNotUnique) {
		t.Fatalf("Replace err = %v, want ErrNotUnique so one occurrence stays the default", err)
	}

	got, err := edit.ReplaceAll(source, "bench.Read(path)", "bench.ReadLog(path)")
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	if strings.Contains(got.Source, "bench.Read(path)") {
		t.Errorf("Source = %q, want no occurrence left", got.Source)
	}
	if n := strings.Count(got.Source, "bench.ReadLog(path)"); n != 2 {
		t.Errorf("replaced %d occurrences, want 2", n)
	}
	if got.Matches != 2 {
		t.Errorf("Matches = %d, want 2", got.Matches)
	}
	if len(got.Ranges) != 2 {
		t.Errorf("Ranges = %v, want one per occurrence", got.Ranges)
	}
}

// Every occurrence still means every occurrence when the anchor reaches
// its sites only through the fuzzy ladder, which is where a
// formatter-respaced file leaves a whole-line anchor.
func TestReplaceAllAcrossFormatterSpacing(t *testing.T) {
	t.Parallel()

	source := "package a\n\nfunc one() {\n\tdo(a,  b)\n}\n\nfunc two() {\n\tdo(a,  b)\n}\n"

	got, err := edit.ReplaceAll(source, "\tdo(a, b)", "\tdo(b, a)")
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	if n := strings.Count(got.Source, "do(b, a)"); n != 2 {
		t.Errorf("Source = %q, want both occurrences replaced", got.Source)
	}
	if got.Matches != 2 {
		t.Errorf("Matches = %d, want 2", got.Matches)
	}
}

// A model asking for `&notUnique` had it arrive as `¬Unique`, the legacy
// HTML reference for the ampersand and "not". It could not write the
// identifier at all: one replay lane sent the same anchor five times and
// died stagnant.
//
// The anchor carries a string literal opening with a word, which is what
// the first repair got wrong: `&quot` collapses to a quote, so putting it
// back rewrote every such literal into `&quotSend`, the repaired anchor
// missed, and the original error stood as though no repair had run.
func TestReplaceRepairsACollapsedEntity(t *testing.T) {
	t.Parallel()

	source := "package a\n\nfunc f() {\n\tif errors.As(err, &notUnique) {\n\t\treturn fail(\"Send it again\")\n\t}\n}\n"

	got, err := edit.Replace(source,
		"\tif errors.As(err, ¬Unique) {\n\t\treturn fail(\"Send it again\")",
		"\tif errors.As(err, ¬Unique) && ok {\n\t\treturn fail(\"Send it again\")")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if !strings.Contains(got.Source, `fail("Send it again")`) {
		t.Errorf("Source = %q, want the string literal left alone", got.Source)
	}

	if !strings.Contains(got.Source, "errors.As(err, &notUnique) && ok {") {
		t.Errorf("Source = %q, want the ampersand put back on both halves", got.Source)
	}
	if strings.Contains(got.Source, "¬") {
		t.Errorf("Source = %q, want no collapsed rune written to the file", got.Source)
	}
	if got.Note == "" {
		t.Error("Note is empty, want the caller told its text arrived mangled")
	}
}

// A rune standing on its own is the symbol the writer meant, not a mangled
// address-of, and repairing it would corrupt the file.
func TestReplaceLeavesALoneEntityRuneAlone(t *testing.T) {
	t.Parallel()

	source := "package a\n\n// ¬ is negation.\nvar x = 1\n"

	got, err := edit.Replace(source, "var x = 1", "var x = 2")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if !strings.Contains(got.Source, "// ¬ is negation.") {
		t.Errorf("Source = %q, want the standalone rune untouched", got.Source)
	}
}
