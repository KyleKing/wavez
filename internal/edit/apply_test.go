package edit_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/edit"
)

func TestApplyToFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")

	if err := os.WriteFile(path, []byte("package foo\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	change, err := edit.ApplyToFile(path, "func a() {}", "func b() {}")
	if err != nil {
		t.Fatalf("ApplyToFile() error = %v", err)
	}

	if change.Path != path {
		t.Errorf("Path = %q, want %q", change.Path, path)
	}

	if change.Added != 1 || change.Removed != 1 {
		t.Errorf("Added=%d Removed=%d, want 1 and 1", change.Added, change.Removed)
	}

	if len(change.Ranges) != 1 || change.Ranges[0].Start != 3 || change.Ranges[0].End != 3 {
		t.Errorf("Ranges = %v, want [{3 3}]", change.Ranges)
	}

	got, err := os.ReadFile(path) // #nosec G304 -- path is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	want := "package foo\n\nfunc b() {}\n"
	if string(got) != want {
		t.Errorf("file content = %q, want %q", got, want)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (no stray temp file)", len(entries))
	}
}

func TestApplyToFile_PreservesMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")

	// #nosec G306 -- test asserts mode 0755 survives ApplyToFile
	if err := os.WriteFile(path, []byte("echo old\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := edit.ApplyToFile(path, "old", "new"); err != nil {
		t.Fatalf("ApplyToFile() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestApplyToFile_RefusesSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")

	if err := os.WriteFile(target, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := edit.ApplyToFile(link, "content", "other")
	if !errors.Is(err, edit.ErrSymlink) {
		t.Fatalf("err = %v, want ErrSymlink", err)
	}

	got, err := os.ReadFile(target) // #nosec G304 -- target is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(got) != "content" {
		t.Errorf("symlink target = %q, want untouched %q", got, "content")
	}
}

func TestApplyToFile_RefusesMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")

	if _, err := edit.ApplyToFile(path, "old", "new"); err == nil {
		t.Fatal("ApplyToFile() error = nil, want an error for a missing file")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat() error = %v, want IsNotExist (must not create the file)", err)
	}
}

func TestApplyToFile_PropagatesReplaceError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	original := "no match here\n"

	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := edit.ApplyToFile(path, "missing text", "replacement")
	if !errors.Is(err, edit.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	got, err := os.ReadFile(path) // #nosec G304 -- path is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(got) != original {
		t.Errorf("file content = %q, want untouched %q", got, original)
	}
}

func TestApplyToFile_AtomicRenameSameDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")

	if err := os.WriteFile(path, []byte("alpha beta\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := edit.ApplyToFile(path, "alpha", "gamma"); err != nil {
		t.Fatalf("ApplyToFile() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}

	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %s must not survive", e.Name())
		}
	}
}
