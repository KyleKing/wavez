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
