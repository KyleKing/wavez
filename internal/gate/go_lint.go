package gate

import (
	"context"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

const (
	lintTool    = "golangci-lint"
	lintTimeout = 5 * time.Minute
	// The typecheckSuffix ends a finding golangci-lint reports when the package
	// does not compile, which is the build check's to say.
	typecheckSuffix = "(typecheck)"
)

// lintFinding matches a finding's location prefix and captures its path.
var lintFinding = regexp.MustCompile(`^(\S+?):\d+(?::\d+)?: `)

// lint runs golangci-lint over the packages holding changed files and keeps
// the findings located in those files, so a change is never blamed for what
// was already there. A project with no golangci-lint configuration has not
// asked for lint and gets no result.
func (r *runner) lint(ctx context.Context, proj project, changed []string) []Result {
	files := present(proj.root, changed)
	if len(files) == 0 || !proj.cfg.Enabled(config.BuiltinLint) || !lintConfigured(proj.root) {
		return nil
	}

	res := Result{Check: config.BuiltinLint, Blocking: true}

	bin, err := r.lookPath(lintTool)
	if err != nil {
		return []Result{res.abstain(lintTool + " not on PATH")}
	}

	args := []string{"run", "--allow-parallel-runners", "--max-issues-per-linter=0", "--max-same-issues=0"}
	for _, dir := range dirsOf(files) {
		args = append(args, pattern(dir))
	}

	out := run(ctx, invocation{dir: proj.root, name: bin, timeout: lintTimeout, args: args})

	switch findings, all := lintFindings(out.output, files); {
	case out.reason != "":
		return []Result{res.abstain(out.reason)}
	case out.exit == 0:
		return []Result{res.pass()}
	case hasLine(out.output, func(line string) bool { return strings.HasSuffix(line, typecheckSuffix) }):
		return []Result{res.abstain("the change does not compile")}
	case len(findings) > 0:
		return []Result{res.fail(strings.Join(findings, "\n"))}
	case all > 0:
		return []Result{res.pass()}
	default:
		return []Result{res.abstain(oneLine(lintError(out.output)))}
	}
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
