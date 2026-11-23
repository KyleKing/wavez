package gate

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

const formatTimeout = 30 * time.Second

// goDiagnostic matches a line a Go tool prints about a place in a source
// file. Its presence is what separates a failure the change caused from a
// toolchain that could not run.
var goDiagnostic = regexp.MustCompile(`^\S+\.go:\d+`)

// goFiles returns the changed Go source files the toolchain treats as
// source. A file under testdata, vendor, or a directory go itself skips is a
// fixture or someone else's code, and formatting or building it would be
// wrong.
func goFiles(changed []string) []string {
	var out []string

	for _, f := range changed {
		if path.Ext(f) != ".go" || slices.ContainsFunc(strings.Split(path.Dir(f), "/"), skippedDir) {
			continue
		}

		out = append(out, f)
	}

	return out
}

func skippedDir(segment string) bool {
	return segment == "testdata" || segment == "vendor" ||
		(len(segment) > 1 && (segment[0] == '_' || segment[0] == '.'))
}

// format rewrites the changed Go files in place with gofmt and reports the
// ones whose bytes changed. A file that does not parse is left alone and
// reported as the failure. It never runs goimports: an agent adds an import
// in one edit and uses it in the next, and goimports would strip it between.
//
// At the edit stage it is a check and says all of that. At the stop stage it
// is only a fixer, run over the whole change set because a file a shell
// command wrote never saw the edit stage: it reports the files it rewrote
// and nothing else, since a file that does not parse is the build's to
// report there.
func (r *runner) format(ctx context.Context, proj project, stage Stage) []Result {
	files := present(proj.root, goFiles(proj.changed))
	if len(files) == 0 || !proj.cfg.Enabled(config.BuiltinFormat) {
		return nil
	}

	res := Result{Check: config.BuiltinFormat, Blocking: true}

	tool, err := r.lookPath("gofmt")
	if err != nil && stage == StageStop {
		return nil
	}

	if err != nil {
		return []Result{res.abstain("gofmt not on PATH")}
	}

	before := hashFiles(proj.root, files)
	out := run(ctx, invocation{
		dir: proj.root, name: tool, timeout: formatTimeout,
		args: append([]string{"-w"}, files...),
	})
	res.Fixed = rewritten(before, hashFiles(proj.root, files))

	switch {
	case stage == StageStop && len(res.Fixed) == 0:
		return nil
	case stage == StageStop:
		return []Result{res.pass()}
	case out.reason != "":
		return []Result{res.abstain(out.reason)}
	case out.exit == 0:
		return []Result{res.pass()}
	case hasLine(out.output, goDiagnostic.MatchString):
		return []Result{res.fail(proj.root, out.output)}
	default:
		return []Result{res.abstain(oneLine(out.output))}
	}
}

// hasLine reports whether any line of text, ignoring indentation, satisfies
// match.
func hasLine(text string, match func(string) bool) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if match(strings.TrimLeft(line, " \t")) {
			return true
		}
	}

	return false
}
