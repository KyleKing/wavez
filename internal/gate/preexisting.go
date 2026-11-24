package gate

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/reduce"
)

// notCompared opens the note on a failure the baseline could not be asked
// about.
const notCompared = "could not ask the tree the session started from: "

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
	// NotBuilt is true when the package did not compile there, so nothing
	// is known about any test in it.
	NotBuilt bool `json:"not_built"`
}

// baselineLines is the reduced output a check gave in the baseline tree,
// empty when it passed there.
type baselineLines struct {
	Lines []string `json:"lines"`
}

// compare sets aside the blocking failures the session did not cause. It
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

// tests sorts the failing tests into the ones that block and the ones that
// do not. A test stops blocking for one of two reasons:
//
//   - It fails in the baseline tree too, where only the failing tests are
//     re-run. A test the baseline does not have passes there by not
//     running, so it is new and stays blocking.
//   - It passes when it is run once more in the session's own tree. A test
//     that fails and then passes on the same bytes is flaky, whoever wrote
//     it, and holding the agent on it teaches the agent nothing.
//
// A baseline package that does not compile says nothing about its tests:
// they stay blocking and the person is told the comparison was not made. A
// test that ran out its timeout is not run a second time.
func (c *comparison) tests(ctx context.Context, res Result) Result {
	failures := reduce.GoTestFailures(res.evidence.output)

	before, reason := c.baselineOf(ctx, res.evidence.again, failures)
	if reason != "" {
		res.Note = notCompared + reason

		return res
	}

	settled := map[string]bool{}
	blocking := false

	for _, failure := range failures {
		known := before[failure.Package]
		if known.NotBuilt {
			res.Note = notCompared + failure.Package + " does not compile there"
		}

		var fresh []string

		for _, name := range topLevel(failure.Tests) {
			switch {
			case known.NotBuilt:
				blocking = true
			case known.failed(failure.Tests, name):
				res.Preexisting = append(res.Preexisting, failure.Package+" "+name)
				settled[failure.Package+" "+name] = true
			case slices.Contains(topLevel(failure.TimedOut), name):
				blocking = true
			default:
				fresh = append(fresh, name)
			}
		}

		flaky := c.flaky(ctx, res.evidence.again, failure.Package, fresh)
		for _, name := range flaky {
			res.Flaky = append(res.Flaky, failure.Package+" "+name)
			settled[failure.Package+" "+name] = true
		}

		blocking = blocking || len(flaky) < len(fresh) || failure.NotBuilt || len(failure.Tests) == 0
	}

	return c.without(res, settled, blocking)
}

// without takes the settled tests out of a failure, and the failure out of
// the way when none is left.
func (c *comparison) without(res Result, settled map[string]bool, blocking bool) Result {
	switch {
	case len(settled) == 0:
	case blocking:
		res = res.fail(c.root, reduce.WithoutGoTests(res.evidence.output, func(pkg, test string) bool {
			return settled[pkg+" "+top(test)]
		}))
	case len(res.Preexisting) > 0:
		res.Status, res.Summary = StatusPreexisting, ""
	default:
		res.Status, res.Summary = StatusPass, ""
	}

	return res
}

// failed reports whether the top-level test and every name that failed
// under it now also failed in the baseline.
func (b baselineTests) failed(names []string, topName string) bool {
	return !slices.ContainsFunc(belowTop(names, topName), func(name string) bool {
		return !slices.Contains(b.Tests[topName], name)
	})
}

// baselineOf asks the baseline about every package that named a failing
// test. A non-empty reason means it could not be asked.
func (c *comparison) baselineOf(
	ctx context.Context, goCmd invocation, failures []reduce.PackageFailure,
) (map[string]baselineTests, string) {
	before := make(map[string]baselineTests, len(failures))

	for _, failure := range failures {
		if failure.NotBuilt || len(failure.Tests) == 0 {
			continue
		}

		known, reason := c.packageTests(ctx, goCmd, failure.Package, topLevel(failure.Tests))
		if reason != "" {
			return nil, reason
		}

		before[failure.Package] = known
	}

	return before, ""
}

// packageTests returns what the named top-level tests of pkg did in the
// baseline tree, from the cache where it has them and from a run where it
// does not. A non-empty reason means the baseline could not be asked.
//
// A test counts as failing there only when it failed twice. One failure
// could be a flaky test's bad run, and kept in the cache it would excuse
// that test for as long as the tree is the baseline. A test that failed
// once and then passed is treated as passing and is not kept either way.
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

	goCmd.dir = dir

	first, reason := c.testsAt(ctx, goCmd, pkg, missing)
	if reason != "" || first.declined {
		// Go declined without a verdict on the package: it is not in the
		// baseline, or its module does not resolve there. Nothing was
		// learned that is worth keeping, and the tests count as new.
		return known, reason
	}

	if first.notBuilt {
		known.NotBuilt = true
		c.cache.store(baselineDir, key, known)

		return known, ""
	}

	// A test that ran out its timeout is not asked twice: the second
	// answer would cost the whole timeout again, and the run has a budget.
	second := first
	if failing := first.finishedFailing(); len(failing) > 0 {
		if second, reason = c.testsAt(ctx, goCmd, pkg, failing); reason != "" {
			return known, reason
		}

		second.failed = append(second.failed, first.timedOut...)
	}

	kept := baselineTests{Tests: maps.Clone(known.Tests)}

	for _, name := range missing {
		twice := belowTop(second.failed, name)
		known.Tests[name] = twice

		// A pass is kept, and so is a failure seen twice.
		if len(twice) > 0 || len(belowTop(first.failed, name)) == 0 {
			kept.Tests[name] = twice
		}
	}

	c.cache.store(baselineDir, key, kept)

	return known, ""
}

// ranTests is what one `go test -run` over named tests said.
type ranTests struct {
	// failed holds every name that failed, subtests included.
	failed []string
	// passed holds the top-level tests go reported as passing.
	passed []string
	// timedOut holds the names in failed that never finished.
	timedOut []string
	// notBuilt is true when the package did not compile.
	notBuilt bool
	// declined is true when go exited non-zero with no verdict on the
	// package.
	declined bool
}

// testsAt runs the named top-level tests of pkg where goCmd points, with
// the same -timeout the session's own run had. A non-empty reason means the
// command gave no answer.
func (c *comparison) testsAt(ctx context.Context, goCmd invocation, pkg string, tests []string) (ranTests, string) {
	quoted := make([]string, len(tests))
	for i, name := range tests {
		quoted[i] = regexp.QuoteMeta(name)
	}

	// -v makes go name the tests that passed: `go test -run` exits zero
	// when the pattern matches nothing, and a test that never ran must not
	// read as one that passed.
	goCmd.args = []string{
		goTest, c.r.testTimeout(), "-count=1", "-v", runFlag, "^(" + strings.Join(quoted, "|") + ")$", pkg,
	}

	out := run(ctx, goCmd)
	if out.reason != "" {
		return ranTests{}, out.reason
	}

	var ran ranTests

	for _, name := range tests {
		if hasLine(out.output, func(line string) bool { return strings.HasPrefix(line, "--- PASS: "+name+" ") }) {
			ran.passed = append(ran.passed, name)
		}
	}

	failures := reduce.GoTestFailures(out.output)
	at := slices.IndexFunc(failures, func(f reduce.PackageFailure) bool { return f.Package == pkg })

	switch {
	case at >= 0:
		ran.failed, ran.timedOut, ran.notBuilt = failures[at].Tests, failures[at].TimedOut, failures[at].NotBuilt
	case out.exit != 0:
		ran.declined = true
	}

	return ran, ""
}

// finishedFailing is the top-level tests that failed by finishing, not by
// running out the clock.
func (r ranTests) finishedFailing() []string {
	return slices.DeleteFunc(topLevel(r.failed), func(name string) bool {
		return slices.Contains(topLevel(r.timedOut), name)
	})
}

// flaky runs the named tests of pkg once more in the session's tree and
// returns the ones that passed this time. A run that gave no answer, or
// that go answered without naming the test as passed, clears nothing.
func (c *comparison) flaky(ctx context.Context, goCmd invocation, pkg string, tests []string) []string {
	if len(tests) == 0 {
		return nil
	}

	goCmd.dir = c.root

	ran, reason := c.testsAt(ctx, goCmd, pkg, tests)
	if reason != "" || ran.notBuilt || ran.declined {
		return nil
	}

	return slices.DeleteFunc(ran.passed, func(name string) bool {
		return slices.Contains(topLevel(ran.failed), name)
	})
}

// same runs the failed check again in the baseline tree and demotes the
// failure when every line of it is one the baseline printed too. It is all
// or nothing: a failure with one new line in it blocks whole, because the
// lines of a build or a linter are not always separable.
func (c *comparison) same(ctx context.Context, res Result) Result {
	ev := res.evidence
	key := cacheKey(append([]string{"lines", c.base.ref(), res.Check}, ev.identity(ctx, c)...)...)

	var before baselineLines

	if !c.cache.load(baselineDir, key, &before) {
		dir, reason := c.tree(ctx)
		if reason == "" {
			before, reason = c.linesAt(ctx, ev, dir, key)
		}

		if reason != "" {
			res.Note = notCompared + reason

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
		lines.Lines = comparableLines(Result{}.fail(c.root, strings.ReplaceAll(out.output, dir, c.root)).Summary)
	}

	c.cache.store(baselineDir, key, lines)

	return lines, ""
}

// identity is what, besides the baseline and the check's name, the
// baseline's answer depends on. A built-in's answer depends on the go that
// gave it, as a test's does.
func (e *evidence) identity(ctx context.Context, c *comparison) []string {
	if e.custom != nil {
		return append([]string{e.custom.check.Command}, e.custom.files...)
	}

	return append([]string{c.toolchain(ctx, e.again.name), e.again.name}, e.again.args...)
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
