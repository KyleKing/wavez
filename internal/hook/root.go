package hook

import (
	"os"
	"path/filepath"
	"strings"
)

// configFile marks a project root outright; the other markers are what a
// project without one is recognized by.
const configFile = ".wavez.toml"

// ProjectDirEnv names the variable Claude Code sets, in every hook's
// environment, to the directory the session started in.
const ProjectDirEnv = "CLAUDE_PROJECT_DIR"

// ResolveRoot finds the project a hook event belongs to, and reports false
// when there is none, in which case the hook has nothing to do and must
// write nothing.
//
// The event's cwd follows the agent as it changes directory, so a root
// taken from it moves mid-session: a `cd tools` into a nested module used
// to start a second state file with a fresh baseline, which made the stop
// gate blind to everything changed before it. The session's own starting
// directory does not move, so it is used when the harness names it: its
// nearest ancestor holding .wavez.toml, or else holding .git, .jj, or go.mod.
// Without it, or when it is in no project, the root is the nearest .wavez.toml above cwd, or else the
// repository cwd is in, or else the nearest go.mod.
//
// A cwd that is not a directory is an event from somewhere that no longer
// exists, and a directory with no marker above it is not a project.
func ResolveRoot(projectDir, cwd string) (string, bool) {
	if !isDir(cwd) {
		return "", false
	}

	if projectDir != "" && isDir(projectDir) {
		dir := filepath.Clean(projectDir)

		if root, ok := nearest(dir, configFile); ok {
			return root, true
		}

		if root, ok := nearest(dir, ".git", jjDir, moduleFile); ok {
			return root, true
		}
	}

	return locate(filepath.Clean(cwd))
}

// FindRoot resolves the project root for a directory named by hand: the
// nearest ancestor holding .wavez.toml, and without one the repository or jj
// workspace the directory is in, and without one the nearest go.mod. A directory with no
// marker above it is its own root.
func FindRoot(dir string) string {
	// A relative directory has no parent to walk up to: the parent of "." is
	// ".", so a run started below the root would take where it stands for
	// the root and find nothing to check.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	dir = filepath.Clean(dir)

	if root, ok := locate(dir); ok {
		return root
	}

	return dir
}

// RepositoryRoot is the top of the repository dir is in, git or jj, which
// is where Claude Code reads a project's settings from.
func RepositoryRoot(dir string) (string, bool) {
	return nearest(filepath.Clean(dir), ".git", jjDir)
}

// jjDir marks the top of a jj workspace, which has no .git when its git
// store is kept inside it.
const jjDir = ".jj"

func locate(dir string) (string, bool) {
	for _, markers := range [][]string{{configFile}, {".git", jjDir}, {moduleFile}} {
		if root, ok := nearest(dir, markers...); ok {
			return root, true
		}
	}

	return "", false
}

func isDir(name string) bool {
	info, err := os.Stat(name)

	return err == nil && info.IsDir()
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
// for a path outside root, which is not the project's to check. A path that
// is not absolute is read from cwd, the directory the tool call ran in.
func relative(root, cwd, path string) (string, bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
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
