package gate

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/reduce"
)

// evidence is what a failed check keeps so that the same question can be
// put to the baseline tree.
type evidence struct {
	// custom is the configured check that failed, or nil for a built-in.
	custom *customCheck
	// output is what the failed command printed, unreduced.
	output string
	// again is a built-in's command to run in the baseline tree. Its dir
	// is filled in once the tree exists.
	again invocation
	// tests marks a `go test` failure, which is compared test by test.
	tests bool
}

// baselineTests is what one package's tests did in the baseline tree.
type baselineTests struct {
	// Tests maps a top-level test that was run there to the names that
	// failed under it, itself included. A test that passed, or that the
	// baseline does not have, maps to an empty list.
	Tests map[string][]string `json:"tests"`
	// NotBuilt is true when the package did not compile there, which no
	// test in it could have survived.
	NotBuilt bool `json:"not_built"`
}

// baselineLines is the reduced output a check gave in the baseline tree,
// empty when it passed there.
type baselineLines struct {
	Lines []string `json:"lines"`
}

// compare demotes the blocking failures the baseline tree already had. It
// costs nothing unless a blocking check failed, and a comparison that could
// not be made leaves the failure blocking with a note for the person.
//
// What is compared: a `go test` failure, test by test; a build failure and a
// configured check's failure, by whether every line of the reduced output
// is one the baseline also printed. Lint is not compared. It already keeps
// only findings in changed files, and a cold golangci-lint run over a second
// tree costs more than the finding does.
func (r *runner) compare(ctx context.Context, proj project, base Baseline, results []Result) []Result {
	if !slices.ContainsFunc(results, func(res Result) bool { return res.blocks() && res.evidence != nil }) {
		return results
	}

	cmp := &comparison{r: r, cache: r.cacheFor(proj.root), base: base, root: proj.root}
	defer cmp.close(ctx)

	for i := range results {
		switch res := results[i]; {
		case !res.blocks() || res.evidence == nil:
		case res.evidence.tests:
			results[i] = cmp.tests(ctx, res)
		default:
			results[i] = cmp.same(ctx, res)
		}
	}

	return results
}

// tests re-runs the failing tests, and only those, in the baseline tree. A
// name that fails there too predates the session. A test the baseline does
// not have passes there by not running, so it is new and stays blocking.
// When the baseline's package does not compile, every failure in that
// package predates the session and no other package's does.
func (c *comparison) tests(ctx context.Context, res Result) Result {
	failures := reduce.GoTestFailures(res.evidence.output)
	before := make(map[string]baselineTests, len(failures))

	for _, failure := range failures {
		if failure.NotBuilt || len(failure.Tests) == 0 {
			continue
		}

		known, reason := c.packageTests(ctx, res.evidence.again, failure.Package, topLevel(failure.Tests))
		if reason != "" {
			res.Note = "could not ask the tree the session started from: " + reason

			return res
		}

		before[failure.Package] = known
	}

	existed := func(pkg, test string) bool {
		known := before[pkg]

		return known.NotBuilt || slices.Contains(known.Tests[top(test)], test)
	}

	blocking := false

	for _, failure := range failures {
		kept := false

		for _, name := range topLevel(failure.Tests) {
			if under(failure.Tests, name, func(test string) bool { return existed(failure.Package, test) }) {
				res.Preexisting = append(res.Preexisting, failure.Package+" "+name)
			} else {
				kept = true
			}
		}

		blocking = blocking || kept || failure.NotBuilt || len(failure.Tests) == 0
	}

	switch {
	case len(res.Preexisting) == 0:
	case blocking:
		res = res.fail(reduce.WithoutGoTests(res.evidence.output, existed))
	default:
		res.Status, res.Summary = StatusPreexisting, ""
	}

	return res
}

// packageTests returns what the named top-level tests of pkg did in the
// baseline tree, from the cache where it has them and from a run where it
// does not. A non-empty reason means the baseline could not be asked.
func (c *comparison) packageTests(
	ctx context.Context, goCmd invocation, pkg string, tests []string,
) (baselineTests, string) {
	key := cacheKey("tests", c.base.ref(), c.toolchain(ctx, goCmd.name), pkg)

	var known baselineTests

	if !c.cache.load(baselineDir, key, &known) || known.Tests == nil {
		known = baselineTests{Tests: map[string][]string{}}
	}

	missing := slices.DeleteFunc(slices.Clone(tests), func(name string) bool {
		_, ok := known.Tests[name]

		return ok
	})
	if known.NotBuilt || len(missing) == 0 {
		return known, ""
	}

	dir, reason := c.tree(ctx)
	if reason != "" {
		return known, reason
	}

	quoted := make([]string, len(missing))
	for i, name := range missing {
		quoted[i] = regexp.QuoteMeta(name)
	}

	goCmd.dir = dir
	goCmd.args = []string{goTest, "-run", "^(" + strings.Join(quoted, "|") + ")$", pkg}

	out := run(ctx, goCmd)
	if out.reason != "" {
		return known, out.reason
	}

	ran := reduce.GoTestFailures(out.output)
	at := slices.IndexFunc(ran, func(f reduce.PackageFailure) bool { return f.Package == pkg })

	switch {
	case at >= 0 && ran[at].NotBuilt:
		known.NotBuilt = true
	case at >= 0:
		for _, name := range missing {
			known.Tests[name] = belowTop(ran[at].Tests, name)
		}
	case out.exit == 0:
		for _, name := range missing {
			known.Tests[name] = []string{}
		}
	default:
		// Go declined without a verdict on the package: it is not in the
		// baseline, or its module does not resolve there. Either way
		// nothing was learned that is worth keeping, and the tests count
		// as new.
		return known, ""
	}

	c.cache.store(baselineDir, key, known)

	return known, ""
}

// same runs the failed check again in the baseline tree and demotes the
// failure when every line of it is one the baseline printed too. It is all
// or nothing: a failure with one new line in it blocks whole, because the
// lines of a build or a linter are not always separable.
func (c *comparison) same(ctx context.Context, res Result) Result {
	ev := res.evidence
	key := cacheKey(append([]string{"lines", c.base.ref(), res.Check}, ev.identity()...)...)

	var before baselineLines

	if !c.cache.load(baselineDir, key, &before) {
		dir, reason := c.tree(ctx)
		if reason == "" {
			before, reason = c.linesAt(ctx, ev, dir, key)
		}

		if reason != "" {
			res.Note = "could not ask the tree the session started from: " + reason

			return res
		}
	}

	now := comparableLines(res.Summary)
	isNew := func(line string) bool { return !slices.Contains(before.Lines, line) }

	if len(now) == 0 || slices.ContainsFunc(now, isNew) {
		return res
	}

	res.Preexisting = []string{oneLine(res.Summary)}
	res.Status, res.Summary = StatusPreexisting, ""

	return res
}

// linesAt runs the check in the baseline tree at dir and returns what it
// printed, reduced the way a failure's summary is.
func (c *comparison) linesAt(ctx context.Context, ev *evidence, dir, key string) (baselineLines, string) {
	inv, ok := ev.at(c.r, dir)
	if !ok {
		return baselineLines{}, ""
	}

	out := run(ctx, inv)

	switch {
	case out.reason != "":
		return baselineLines{}, out.reason
	case out.exit == exitNotFound, out.exit == exitNotExecutable:
		// The baseline tree lacks something the check needs to start.
		// That is not the check's answer, so it is not kept.
		return baselineLines{}, ""
	}

	var lines baselineLines

	if out.exit != 0 {
		// A path under the baseline tree is the same file under the root.
		lines.Lines = comparableLines(Result{}.fail(strings.ReplaceAll(out.output, dir, c.root)).Summary)
	}

	c.cache.store(baselineDir, key, lines)

	return lines, ""
}

// identity is what, besides the baseline and the check's name, the
// baseline's answer depends on.
func (e *evidence) identity() []string {
	if e.custom != nil {
		return append([]string{e.custom.check.Command}, e.custom.files...)
	}

	return append([]string{e.again.name}, e.again.args...)
}

// at returns the command to run in the baseline tree at dir, and false when
// there is nothing to run there: a check whose files the baseline does not
// have, or no shell.
func (e *evidence) at(r *runner, dir string) (invocation, bool) {
	if e.custom == nil {
		inv := e.again
		inv.dir = dir

		return inv, true
	}

	files := present(dir, e.custom.files)
	if len(files) == 0 && strings.Contains(e.custom.check.Command, filesToken) {
		return invocation{}, false
	}

	sh, err := r.lookPath("sh")
	if err != nil {
		return invocation{}, false
	}

	return invocation{
		dir: dir, name: sh, timeout: e.custom.check.Timeout,
		args: []string{"-c", strings.ReplaceAll(e.custom.check.Command, filesToken, shellQuote(files))},
	}, true
}

// comparableLines splits a summary into the lines two runs of one failure
// share: trimmed, without blanks, and without the elapsed times that differ
// between any two runs.
func comparableLines(summary string) []string {
	var out []string

	for line := range strings.SplitSeq(elapsed.ReplaceAllString(summary, ""), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}

	return out
}

// top is the top-level test a test or subtest name belongs to.
func top(test string) string {
	name, _, _ := strings.Cut(test, "/")

	return name
}

// topLevel returns the distinct top-level tests of names, in order.
func topLevel(names []string) []string {
	var out []string

	for _, name := range names {
		if t := top(name); !slices.Contains(out, t) {
			out = append(out, t)
		}
	}

	return out
}

// belowTop returns the names that are the top-level test or its subtests.
func belowTop(names []string, topName string) []string {
	out := []string{}

	for _, name := range names {
		if top(name) == topName {
			out = append(out, name)
		}
	}

	return out
}

// under reports whether is holds of every name at or below the top-level
// test.
func under(names []string, topName string, is func(test string) bool) bool {
	return !slices.ContainsFunc(belowTop(names, topName), func(name string) bool { return !is(name) })
}
