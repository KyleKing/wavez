package gate

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/config"
)

const (
	lintTool = "golangci-lint"
	// The typecheckSuffix ends a finding golangci-lint reports when the package
	// does not compile, which is the build check's to say.
	typecheckSuffix = "(typecheck)"
)

// lintCacheEnv names the variable that tells golangci-lint where its cache
// is.
const lintCacheEnv = "GOLANGCI_LINT_CACHE"

// lintEnv gives golangci-lint a cache of the project's own, unless the
// person chose one. Its results cache is keyed by package content and holds
// absolute paths, so two checkouts with the same bytes (the baseline tree,
// a benchmark worktree) that share the default cache replay each other's
// paths, and a finding reported under another checkout's path matches no
// changed file here and reads as a pass.
func lintEnv(root string) []string {
	if os.Getenv(lintCacheEnv) != "" {
		return nil
	}

	dir := filepath.Join(root, filepath.FromSlash(cacheDir), lintCacheDir)
	if !prepareCache(root, dir) {
		return nil
	}

	return []string{lintCacheEnv + "=" + dir}
}

// lintFinding matches a finding's location prefix and captures its path.
var lintFinding = regexp.MustCompile(`^(\S+?):\d+(?::\d+)?: `)

// lint runs golangci-lint over the packages holding changed files and keeps
// the findings located in those files, so a change is never blamed for what
// was already there. Importers are not linted: a finding in one would sit
// in a file the change did not touch, and that is never reported. A project
// with no golangci-lint configuration has not asked for lint and gets no
// result.
//
// A package whose inputs are byte for byte those of an earlier pass is not
// linted again, and a run where every package was is a cached pass.
func (r *runner) lint(ctx context.Context, proj project, graph *lazyGraph, changed []string) []Result {
	files := present(proj.root, changed)
	if len(files) == 0 || !proj.cfg.Enabled(config.BuiltinLint) || !lintConfigured(proj.root) {
		return nil
	}

	res := Result{Check: config.BuiltinLint, Blocking: true}

	bin, err := r.lookPath(lintTool)
	if err != nil {
		return []Result{res.abstain(lintTool + " not on PATH")}
	}

	cache := r.cacheFor(proj.root)
	keys := map[string]string{}

	if !cache.off {
		if goBin, g, reason := graph.get(ctx); reason == "" {
			keys = lintKeys(proj.root, bin, goBin, g, files)
		}
	}

	var pending []string

	for _, dir := range dirsOf(files) {
		if !cache.passed(keys[dir]) {
			pending = append(pending, dir)
		}
	}

	if len(pending) == 0 {
		res.Cached = true

		return []Result{res.pass()}
	}

	res, failed := r.lintRun(ctx, proj.root, bin, pending, files, res)
	if res.Status == StatusPass || res.Status == StatusFail {
		for _, dir := range pending {
			if !slices.Contains(failed, dir) {
				cache.pass(keys[dir])
			}
		}
	}

	return []Result{res}
}

// lintRun runs golangci-lint over dirs and reads its answer for files, with
// the directories of the files it found something in.
func (r *runner) lintRun(
	ctx context.Context, root, bin string, dirs, files []string, res Result,
) (Result, []string) {
	args := slices.Grow(
		[]string{"run", "--allow-parallel-runners", "--max-issues-per-linter=0", "--max-same-issues=0"}, len(dirs),
	)

	for _, dir := range dirs {
		args = append(args, pattern(dir))
	}

	out := run(ctx, invocation{
		dir: root, name: bin, timeout: r.commandTimeout(), args: args, env: lintEnv(root),
	})

	switch findings, all := lintFindings(out.output, files); {
	case out.reason != "":
		return res.abstain(out.reason), nil
	case out.exit == 0:
		return res.pass(), nil
	case hasLine(out.output, func(line string) bool { return strings.HasSuffix(line, typecheckSuffix) }):
		return res.abstain("the change does not compile"), nil
	case len(findings) > 0:
		return res.fail(root, strings.Join(findings, "\n")), findingDirs(findings)
	case all > 0:
		return res.pass(), nil
	default:
		return res.abstain(oneLine(lintError(out.output))), nil
	}
}

// findingDirs returns the directories of the files findings are in.
func findingDirs(findings []string) []string {
	var out []string

	for _, line := range findings {
		if m := lintFinding.FindStringSubmatch(line); m != nil {
			if dir := path.Dir(path.Clean(filepath.ToSlash(m[1]))); !slices.Contains(out, dir) {
				out = append(out, dir)
			}
		}
	}

	return out
}

// lintFindings returns the finding lines located in files and how many
// finding lines there were in all. The source excerpt and caret the linter
// prints under each finding are dropped.
func lintFindings(output string, files []string) ([]string, int) {
	var (
		kept []string
		all  int
	)

	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)

		m := lintFinding.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		all++

		if slices.Contains(files, path.Clean(filepath.ToSlash(m[1]))) {
			kept = append(kept, line)
		}
	}

	return kept, all
}

// lintError picks the line that says why golangci-lint could not run, which
// it logs as an error after any warnings.
func lintError(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, "level=error") || strings.HasPrefix(line, "Error:") {
			return line
		}
	}

	return output
}

func lintConfigured(root string) bool {
	for _, ext := range []string{"toml", "yml", "yaml", "json"} {
		if isFile(filepath.Join(root, ".golangci."+ext)) {
			return true
		}
	}

	return false
}
