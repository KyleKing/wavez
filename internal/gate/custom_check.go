package gate

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/reduce"
)

const (
	// The filesToken is replaced in a command by the matched files.
	filesToken = "{files}"
	// The summaryLimit bounds one check's failure text. A reduced failure is a
	// few hundred bytes; this is the ceiling for one that would not reduce.
	summaryLimit = 3000
)

// customCheck is a configured check a run selected, with the files that
// selected it and still exist.
type customCheck struct {
	files []string
	fixed []string
	check config.Check
	// hit is true when a pass on these bytes is cached.
	hit bool
}

// selectChecks returns the configured checks for stage that the changed
// files match. A check whose command names {files} needs one that still
// exists; any other check runs on a deletion too.
func selectChecks(proj project, stage Stage) []customCheck {
	var out []customCheck

	for i := range proj.cfg.Checks {
		check := &proj.cfg.Checks[i]
		if Stage(check.Stage) != stage {
			continue
		}

		matched := check.Select(proj.changed)
		if len(matched) == 0 {
			continue
		}

		files := present(proj.root, matched)
		if len(files) == 0 && strings.Contains(check.Command, filesToken) {
			continue
		}

		out = append(out, customCheck{check: *check, files: files})
	}

	return out
}

// fix runs the check's fixer, if it has one, and returns the matched files
// whose bytes it changed. The fixer's exit status is ignored: one that could
// not repair everything exits non-zero, and what it left is the check's to
// report.
func (r *runner) fix(ctx context.Context, root string, c customCheck) []string {
	if c.check.Fix == "" || (len(c.files) == 0 && strings.Contains(c.check.Fix, filesToken)) {
		return nil
	}

	sh, err := r.lookPath("sh")
	if err != nil {
		return nil
	}

	before := hashFiles(root, c.files)

	run(ctx, invocation{
		dir: root, name: sh, timeout: c.check.Timeout,
		args: []string{"-c", strings.ReplaceAll(c.check.Fix, filesToken, shellQuote(c.files))},
	})

	return rewritten(before, hashFiles(root, c.files))
}

// command runs the check's command and reads its exit status: zero passes,
// the shell's own "not found" and "not executable" statuses abstain, and
// anything else is a failure carrying the reduced output. A pass is
// remembered only for a check that declared its inputs, under the key of
// the bytes it was run on, and a cached one runs nothing.
func (r *runner) command(ctx context.Context, proj project, c customCheck) Result {
	root := proj.root
	res := Result{Check: c.check.Name, Blocking: c.check.Level != config.LevelAdvise, Fixed: c.fixed}

	if c.hit {
		res.Cached = true

		return res.pass()
	}

	sh, err := r.lookPath("sh")
	if err != nil {
		return res.abstain("sh not on PATH")
	}

	key := customKey(proj, c)

	out := run(ctx, invocation{
		dir: root, name: sh, timeout: c.check.Timeout,
		args: []string{"-c", strings.ReplaceAll(c.check.Command, filesToken, shellQuote(c.files))},
	})

	switch {
	case out.reason != "":
		return res.abstain(out.reason)
	case out.exit == 0:
		r.cacheFor(root).pass(key)

		return res.pass()
	case out.exit == exitNotFound, out.exit == exitNotExecutable:
		return res.abstain(oneLine(out.output))
	default:
		res.evidence = &evidence{custom: &c, output: out.output}

		return res.fail(root, out.output)
	}
}

func (r Result) pass() Result {
	r.Status = StatusPass

	return r
}

func (r Result) abstain(reason string) Result {
	r.Status, r.Summary = StatusAbstained, reason

	return r
}

// fail records output as the failure, reduced to the lines that name it,
// with paths relative to root, and bounded so one check cannot spend the
// reader's whole budget.
func (r Result) fail(root, output string) Result {
	r.Status = StatusFail
	r.Summary = reduce.Failure(output, root, summaryLimit)

	return r
}

// hashFiles returns the content hash of each file that can be read.
func hashFiles(root string, files []string) map[string][sha256.Size]byte {
	hashes := make(map[string][sha256.Size]byte, len(files))
	dir := os.DirFS(root)

	for _, f := range files {
		data, err := fs.ReadFile(dir, f)
		if err != nil {
			continue
		}

		hashes[f] = sha256.Sum256(data)
	}

	return hashes
}

// rewritten returns the files whose hash differs between before and after,
// sorted.
func rewritten(before, after map[string][sha256.Size]byte) []string {
	var out []string

	for f, sum := range before {
		if now, ok := after[f]; ok && now != sum {
			out = append(out, f)
		}
	}

	slices.Sort(out)

	return out
}
