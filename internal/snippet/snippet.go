// Package snippet loads and saves the composer's saved phrases: name to
// literal text, stored as JSON rather than pkl because the composer writes
// them back and pkl is for configuration a human authors by hand.
package snippet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kyleking/wavez/internal/config"
)

// FileName is the snippets file's name, used both beside a project's
// ".wavez.pkl" and under the per-laptop user config directory.
const FileName = "snippets.json"

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// RepoPath is the per-repo snippets file, beside root's ".wavez.pkl" so a
// project's saved phrases travel with the project.
func RepoPath(root string) string {
	return filepath.Join(root, FileName)
}

// UserPath is the per-laptop snippets file, under wavez's user config
// directory so personal habits do not travel with a project.
func UserPath() (string, error) {
	dir, err := config.UserDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}

	return filepath.Join(dir, FileName), nil
}

// LoadAll returns root's merged snippets: the per-laptop file and root's
// per-repo file, keyed by name. A name in both files takes the repo file's
// text, since a project's conventions should win over a personal habit of
// the same name. A missing file is not an error.
func LoadAll(root string) (map[string]string, error) {
	out := map[string]string{}

	userPath, err := UserPath()
	if err != nil {
		return nil, err
	}

	user, err := Load(userPath)
	if err != nil {
		return nil, err
	}

	for name, text := range user {
		out[name] = text
	}

	repo, err := Load(RepoPath(root))
	if err != nil {
		return nil, err
	}

	for name, text := range repo {
		out[name] = text
	}

	return out, nil
}

// Load returns path's snippets, or an empty map with no error when path
// does not exist.
func Load(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a snippets file this package owns
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}

		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var snippets map[string]string
	if err := json.Unmarshal(data, &snippets); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return snippets, nil
}

// Save writes snippets to path atomically: a temp file in the same
// directory, then a rename, so a reader never observes a partial write.
func Save(path string, snippets map[string]string) error {
	data, err := json.MarshalIndent(snippets, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding snippets: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

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

	if err := tmp.Chmod(filePerm); err != nil {
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
