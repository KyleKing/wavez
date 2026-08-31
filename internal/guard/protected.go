package guard

import (
	"path/filepath"
	"strings"
)

// repoInternals are directories that hold a version-control system's own
// state wherever they appear, so a nested checkout's hooks are protected
// the same as this one's. `.git/config` alone can point `core.hooksPath`
// somewhere else or carry a `hook.*.command`, so the whole directory is
// covered rather than `hooks/`.
var repoInternals = []string{".git", ".jj"}

// rootProtected are the paths, relative to the project root, that decide
// what a later command may do without asking: the approvals themselves, the
// config carrying `shellAllow` and each routine's argv, and the three
// places a body runs off an otherwise innocuous command (`git commit`,
// `mise run`, a push). Every filename mise loads a project config or a file
// task from is here, because `mise run` names a task and never the file the
// task body sits in: covering one spelling and not the rest would read as a
// fence while leaving the same command open.
//
// Their names are only meaningful at the root, so a fixture under testdata
// spelled `hk.pkl` is ordinary work and is left alone.
var rootProtected = []string{
	".config/mise.toml",
	".config/mise/conf.d",
	".config/mise/config.toml",
	".config/mise/tasks",
	".github/workflows",
	".mise-tasks",
	".mise.local.toml",
	".mise.toml",
	".mise/config.toml",
	".mise/tasks",
	".wavez.pkl",
	".wavez/approvals.jsonl",
	"hk.pkl",
	"mise-tasks",
	"mise.local.toml",
	"mise.toml",
	"mise/config.toml",
	"mise/tasks",
}

// ReasonProtected is what a refused write is told, once, wherever the
// refusal is raised.
const ReasonProtected = "governs what a later command may run without asking, so no tool may write it; " +
	"ask the user to make this change by hand"

// ProtectedWrite names the protected location abs falls under, empty when
// none does, judged against the project root abs was resolved from.
//
// It is a floor rather than a policy: a write here widens what every later
// run may do with no prompt, and it does so silently, since the file that
// grants the permission is not the file that later uses it. Nothing a run
// legitimately does needs to write one of these, so refusing costs nothing
// and the escalation it stops is permanent.
func ProtectedWrite(root, abs string) string {
	rel, err := filepath.Rel(cleanRoot(root), filepath.Clean(abs))
	if err != nil {
		return ""
	}

	return protectedRel(filepath.ToSlash(rel))
}

// protectedRel answers for a slash-separated path already known to be
// inside the project root.
func protectedRel(rel string) string {
	if rel == "." || strings.HasPrefix(rel, "../") {
		return ""
	}

	for _, part := range strings.Split(rel, "/") {
		for _, dir := range repoInternals {
			if part == dir {
				return dir + "/"
			}
		}
	}

	for _, p := range rootProtected {
		if rel == p {
			return p
		}

		if strings.HasPrefix(rel, p+"/") {
			return p + "/"
		}
	}

	return ""
}
