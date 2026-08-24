package edit_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/edit"
)

// A batch is all or nothing: one bad anchor must leave the file as it was,
// or a caller has to reason about a half-applied edit it cannot see.
func TestApplyAllToFile(t *testing.T) {
	t.Parallel()

	source := "package p\n\nfunc A() {}\n\nfunc B() {}\n"

	write := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "f.go")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		return path
	}

	t.Run("every pair lands and the change spans them all", func(t *testing.T) {
		t.Parallel()

		path := write(t)
		change, err := edit.ApplyAllToFile(path, []edit.Pair{
			{OldString: "func A() {}", NewString: "func A() error { return nil }"},
			{OldString: "func B() {}", NewString: "func B() error { return nil }"},
		})
		if err != nil {
			t.Fatalf("ApplyAllToFile: %v", err)
		}

		data, err := os.ReadFile(path) // #nosec G304 -- path is a t.TempDir() fixture
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if got := string(data); !strings.Contains(got, "func A() error") || !strings.Contains(got, "func B() error") {
			t.Errorf("file = %q, want both edits applied", got)
		}
		if len(change.Ranges) != 1 || change.Ranges[0].Start != 3 || change.Ranges[0].End != 5 {
			t.Errorf("Ranges = %+v, want one span covering lines 3-5", change.Ranges)
		}
	})

	t.Run("a pair that misses leaves the file untouched", func(t *testing.T) {
		t.Parallel()

		path := write(t)
		_, err := edit.ApplyAllToFile(path, []edit.Pair{
			{OldString: "func A() {}", NewString: "func A() error { return nil }"},
			{OldString: "func nothing() {}", NewString: "x"},
		})
		if !errors.Is(err, edit.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "edit 2 of 2") {
			t.Errorf("err = %v, want it to name which edit failed", err)
		}

		data, err := os.ReadFile(path) // #nosec G304 -- path is a t.TempDir() fixture
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != source {
			t.Errorf("file = %q, want it unchanged", data)
		}
	})
}

// A batch's anchors are written against the file the caller read, so that
// is what they resolve against. Applying them in sequence made the second
// anchor search a file no caller had seen: measured over this project's
// thread logs, a batch of one fails 27% of the time and a batch of two 67%,
// and the extra failures are anchors an earlier pair had already consumed.
func TestApplyAllResolvesEveryAnchorAgainstTheFileAsRead(t *testing.T) {
	t.Parallel()

	const src = "package a\n\nimport \"fmt\"\n\nfunc F() {\n\tfmt.Println(\"one\")\n}\n"

	path := writeTemp(t, src)

	change, err := edit.ApplyAllToFile(path, []edit.Pair{
		// The first pair rewrites the import, which under sequential
		// application shifted everything the second pair anchors on.
		{OldString: "import \"fmt\"", NewString: "import (\n\t\"fmt\"\n\t\"os\"\n)"},
		{OldString: "fmt.Println(\"one\")", NewString: "fmt.Fprintln(os.Stdout, \"one\")"},
	})
	if err != nil {
		t.Fatalf("ApplyAllToFile: %v", err)
	}

	got := readFile(t, path)
	for _, want := range []string{"\"os\"", "fmt.Fprintln(os.Stdout, \"one\")"} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q:\n%s", want, got)
		}
	}

	if change.Added == 0 {
		t.Error("Change reported no added lines")
	}
}

// Two edits over the same text is the one case sequencing hid behind a
// misleading "not found": the second anchor was gone because the first had
// replaced it. Which one wins is undecidable and is refused by index.
func TestApplyAllRefusesOverlappingEditsByIndex(t *testing.T) {
	t.Parallel()

	path := writeTemp(t, "package a\n\nfunc F() int { return 1 }\n")

	_, err := edit.ApplyAllToFile(path, []edit.Pair{
		{OldString: "func F() int { return 1 }", NewString: "func F() int { return 2 }"},
		{OldString: "return 1", NewString: "return 3"},
	})
	if err == nil {
		t.Fatal("overlapping edits were applied")
	}

	var overlap *edit.OverlapError
	if !errors.As(err, &overlap) {
		t.Fatalf("err = %v, want an OverlapError", err)
	}

	if overlap.First != 1 || overlap.Second != 2 {
		t.Errorf("OverlapError = %+v, want it to name edits 1 and 2", overlap)
	}

	if got := readFile(t, path); !strings.Contains(got, "return 1") {
		t.Errorf("the file changed despite the refusal:\n%s", got)
	}
}

func writeTemp(t *testing.T, source string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // path is this test's own temp file
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	return string(body)
}
