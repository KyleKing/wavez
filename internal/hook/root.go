package hook

import (
	"os"
	"path/filepath"
	"strings"
)

// configFile marks a project root outright; the other markers are what a
// project without one is recognized by.
const configFile = ".wavez.toml"

// FindRoot resolves the project root for an event fired in dir: the nearest
// ancestor holding .wavez.toml, and without one the nearest holding .git or
// go.mod, so a nested module inside a configured project does not become a
// root of its own. A directory with no marker above it is its own root.
func FindRoot(dir string) string {
	dir = filepath.Clean(dir)

	if root, ok := nearest(dir, configFile); ok {
		return root
	}

	if root, ok := nearest(dir, ".git", "go.mod"); ok {
		return root
	}

	return dir
}

// nearest walks up from dir to the first directory holding one of names.
func nearest(dir string, names ...string) (string, bool) {
	for {
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return dir, true
			}
		}

		parent := filepath.Dir(dir)
		// filepath.Dir of "." is "." and of "/" is "/": the walk ends
		// where the parent stops changing, not where it reaches a slash.
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

// relative returns path as a slash-separated path under root, and false
// for a path outside root, which is not the project's to check.
func relative(root, path string) (string, bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}

	rel, ok := under(root, path)
	if ok {
		return rel, true
	}

	// The harness may name the file through a symlink the root was
	// resolved past, or the reverse (/tmp and /private/tmp on macOS).
	realRoot, rootErr := filepath.EvalSymlinks(root)
	realDir, dirErr := filepath.EvalSymlinks(filepath.Dir(path))

	if rootErr != nil || dirErr != nil {
		return "", false
	}

	return under(realRoot, filepath.Join(realDir, filepath.Base(path)))
}

func under(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return filepath.ToSlash(rel), true
}
