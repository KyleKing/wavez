package gate

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

const (
	// The goTest subcommand compiles a package's tests and runs them.
	goTest = "test"

	buildTimeout = 3 * time.Minute
	testTimeout  = 5 * time.Minute
)

var (
	// The testVerdict pattern matches the line go test prints for a package whose tests
	// ran and failed. A package that never compiled or never loaded gets a
	// bracketed note after a space instead of a tab and an elapsed time.
	testVerdict = regexp.MustCompile(`^FAIL\t\S+\t`)
	// The buildVerdict pattern matches the line go test prints for a package whose
	// test binary did not compile.
	buildVerdict = regexp.MustCompile(`^FAIL\t\S+ \[build failed\]`)
)

// goStop runs the stop-stage Go built-ins: build, then test and lint side by
// side. A build failure is reported alone, since every test and finding
// after it would restate the same errors.
func (r *runner) goStop(ctx context.Context, proj project) []Result {
	files := goFiles(proj.changed)
	wholeModule := slices.Contains(proj.changed, "go.mod") || slices.Contains(proj.changed, "go.sum")

	if len(files) == 0 && !wholeModule {
		return nil
	}

	var (
		compiled, linted []Result
		wg               sync.WaitGroup
	)

	wg.Go(func() { compiled = r.goCompile(ctx, proj, files, wholeModule) })
	wg.Go(func() { linted = r.lint(ctx, proj, files) })
	wg.Wait()

	if slices.ContainsFunc(compiled, func(res Result) bool { return res.Status == StatusFail }) &&
		slices.ContainsFunc(linted, func(res Result) bool { return res.Status == StatusAbstained }) {
		linted = nil
	}

	return append(compiled, linted...)
}

// goCompile produces the build and test results. Non-test code is built
// first because that is the fast way to the common failure. Test code is
// compiled by the test run itself, so a test binary is linked once: when
// that run reports packages that did not build, the failure belongs to
// build and test has nothing to add.
func (r *runner) goCompile(ctx context.Context, proj project, files []string, wholeModule bool) []Result {
	build := Result{Check: config.BuiltinBuild, Blocking: true}
	test := Result{Check: config.BuiltinTest, Blocking: true}
	wantBuild, wantTest := proj.cfg.Enabled(build.Check), proj.cfg.Enabled(test.Check)

	if !wantBuild && !wantTest {
		return nil
	}

	goBin, pkgs, reason := r.goScope(ctx, proj.root, files, wholeModule)
	if reason != "" {
		return enabled(wantBuild, build.abstain(reason), wantTest, test.abstain(reason))
	}

	if len(pkgs.all) == 0 {
		return nil
	}

	if wantBuild && len(pkgs.buildable) > 0 {
		inv := invocation{
			dir: proj.root, name: goBin, timeout: buildTimeout, args: append([]string{"build"}, pkgs.buildable...),
		}
		if build = classifyGo(build, run(ctx, inv), evidence{again: inv}); build.Status != StatusPass {
			return []Result{build}
		}
	}

	// The compileOnly arguments build the test binaries and run nothing,
	// which is also how a build failure found by the test run is put to
	// the baseline tree.
	compileOnly := []string{goTest, "-run", "^$"}

	args := []string{goTest}
	if !wantTest {
		compileOnly = append(compileOnly, "-vet=off")
		args = compileOnly
	}

	inv := invocation{dir: proj.root, name: goBin, timeout: testTimeout, args: slices.Concat(args, pkgs.all)}
	out := run(ctx, inv)

	if wantBuild && (!wantTest || hasLine(out.output, buildVerdict.MatchString)) {
		inv.args = slices.Concat(compileOnly, pkgs.all)

		return []Result{classifyGo(build, out, evidence{again: inv})}
	}

	test = classifyGo(test, out, evidence{again: inv, tests: true})

	switch {
	case build.Status != "":
	case test.Status == StatusAbstained:
		build = build.abstain(test.Summary)
	default:
		build = build.pass()
	}

	return enabled(wantBuild, build, wantTest, test)
}

// goScope finds the go binary and the package patterns the change affects.
// A non-empty reason means neither could be established.
func (r *runner) goScope(
	ctx context.Context, root string, files []string, wholeModule bool,
) (string, scope, string) {
	goBin, err := r.lookPath("go")
	if err != nil {
		return "", scope{}, "go not on PATH"
	}

	if wholeModule {
		return goBin, scope{all: []string{wholeModulePattern}, buildable: []string{wholeModulePattern}}, ""
	}

	graph, reason := loadGraph(ctx, goBin, root)
	if reason != "" {
		return "", scope{}, reason
	}

	return goBin, graph.affected(files), ""
}

func enabled(wantBuild bool, build Result, wantTest bool, test Result) []Result {
	var out []Result
	if wantBuild {
		out = append(out, build)
	}

	if wantTest {
		out = append(out, test)
	}

	return out
}

// classifyGo reads a go build or go test run. A non-zero exit is the
// change's failure only when the output points at source or reports a test
// that ran; anything else is the toolchain declining to run (a module that
// does not resolve, a pattern that matches nothing), which abstains with
// go's own first line as the reason. A failure keeps ev, so that it can be
// compared with the baseline.
func classifyGo(res Result, out outcome, ev evidence) Result {
	switch {
	case out.reason != "":
		return res.abstain(out.reason)
	case out.exit == 0:
		return res.pass()
	case hasLine(out.output, sourceFailure):
		ev.output = out.output
		res.evidence = &ev

		return res.fail(out.output)
	default:
		return res.abstain(oneLine(out.output))
	}
}

func sourceFailure(line string) bool {
	return goDiagnostic.MatchString(line) || testVerdict.MatchString(line) ||
		strings.HasPrefix(line, "--- FAIL") || strings.HasPrefix(line, "panic:") ||
		strings.Contains(line, "import cycle not allowed") || strings.HasPrefix(line, "found packages ")
}
