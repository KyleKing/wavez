package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// UserDir is the per-laptop directory for files that belong to the machine
// rather than to any one project (the daemon socket, model settings, and
// personal snippets), under the OS's user config directory.
func UserDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}

	return filepath.Join(dir, "wavez"), nil
}

// UserSocketPath is wavezd's default unix socket, one per laptop rather
// than one per project, so a single daemon can load several project roots
// behind one address.
func UserSocketPath() (string, error) {
	dir, err := UserDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "d.sock"), nil
}
