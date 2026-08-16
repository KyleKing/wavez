package edit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kyleking/wavez/internal/tool"
)

// ErrSymlink is returned when ApplyToFile is asked to edit a symlink.
var ErrSymlink = errors.New("refusing to follow a symlink")

// ApplyToFile reads path, replaces oldString with newString via Replace, and
// writes the result back atomically: a temp file in the same directory,
// then a rename. It refuses to follow a symlink at path and never creates a
// file that does not already exist.
func ApplyToFile(path, oldString, newString string) (tool.Change, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return tool.Change{}, fmt.Errorf("stat %s: %w", path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return tool.Change{}, fmt.Errorf("%s: %w", path, ErrSymlink)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path is the file this tool exists to edit
	if err != nil {
		return tool.Change{}, fmt.Errorf("reading %s: %w", path, err)
	}

	result, err := Replace(string(data), oldString, newString)
	if err != nil {
		return tool.Change{}, err
	}

	if err := writeAtomic(path, info.Mode(), []byte(result.Source)); err != nil {
		return tool.Change{}, fmt.Errorf("writing %s: %w", path, err)
	}

	return tool.Change{
		Path:    path,
		Added:   result.Added,
		Removed: result.Removed,
		Ranges:  result.Ranges,
	}, nil
}

func writeAtomic(path string, mode os.FileMode, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // no-op once the rename below succeeds; ENOENT otherwise

	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck,gosec // already failing on the write error below
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close() //nolint:errcheck,gosec // already failing on the chmod error below
		return fmt.Errorf("setting temp file mode: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}
