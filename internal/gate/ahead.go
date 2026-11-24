package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// BuildAhead compiles the Go packages named by pkgs, patterns relative to
// root, and their tests, with the flags the test check uses, so that the
// check's first run after an edit finds everything but the edited package
// in the build cache. It runs nothing and reports nothing: a package that
// does not compile is the check's to say, and a tree with no Go toolchain
// is left as it was.
func BuildAhead(ctx context.Context, root string, pkgs []string) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return
	}

	args := []string{goTest, trimpath, runFlag, "^$"}
	if noop, err := exec.LookPath("true"); err == nil {
		args = append(args, "-exec", noop)
	}

	// A package is built from its module's directory, which need not be the
	// project's root.
	for dir, patterns := range byModule(root, pkgs) {
		//nolint:gosec // the go tool, and package patterns under root
		cmd := exec.CommandContext(ctx, goBin, append(args, patterns...)...)
		cmd.Dir = dir
		_ = cmd.Run() //nolint:errcheck // the build cache is the result
	}
}

// byModule groups package patterns by the directory of the go.mod above
// each, rewritten relative to it. A pattern with no go.mod at or below root
// is left out.
func byModule(root string, pkgs []string) map[string][]string {
	out := map[string][]string{}

	for _, pkg := range pkgs {
		if !filepath.IsLocal(filepath.FromSlash(pkg)) {
			continue
		}

		dir := filepath.Join(root, filepath.FromSlash(pkg))

		for mod := dir; ; mod = filepath.Dir(mod) {
			if _, err := os.Stat(filepath.Join(mod, "go.mod")); err == nil {
				if rel, err := filepath.Rel(mod, dir); err == nil {
					out[mod] = append(out[mod], "./"+filepath.ToSlash(rel))
				}

				break
			}

			if mod == root || mod == filepath.Dir(mod) {
				break
			}
		}
	}

	return out
}
