package gate

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

// DefaultStopBudget bounds one whole stop-stage run: the fixers, every
// check, and the comparison with the tree the session started from.
//
// The harness's prompt cache goes cold after about five minutes without a
// request, and a stop that holds the turn past that makes the next request
// re-read the whole conversation at full price. Half of that window is
// spent here at most, which leaves the other half for the agent's own next
// step, and is still several times what build, test, and lint of a changed
// package take on a warm build cache. Work that needs longer is a job for
// CI, not for the end of every turn.
const DefaultStopBudget = 150 * time.Second

const (
	// The goTest subcommand compiles a package's tests and runs them.
	goTest = "test"
	// A test binary gets testShare of the budget before `go test -timeout`
	// ends it with a panic that names the tests still running. One Go
	// command gets commandShare before its process is killed, which only
	// says that it timed out. The gap between the two is what turns a
	// hanging test into a failure with a name instead of an abstention.
	testShare    = 2
	commandShare = 3
	budgetParts  = 5
	// The quietFormat template prints nothing for a package. An empty -f
	// would mean go list's default, which prints the import path.
	quietFormat = `{{""}}`
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

// commandTimeout is how long one Go command may run.
func (r *runner) commandTimeout() time.Duration {
	return r.budget * commandShare / budgetParts
}

// testTimeout is the -timeout handed to `go test`, in the session tree and
// in the baseline tree alike.
func (r *runner) testTimeout() string {
	return "-timeout=" + (r.budget * testShare / budgetParts).Round(time.Second).String()
}

// goJobCount is how many jobs goJobs returns when it returns any: compile,
// then lint.
const goJobCount = 2

// goJobs returns the stop-stage Go built-ins as two jobs: build then test,
// and lint. Both read one import graph, listed once by whichever asks first.
// A change that selects no Go package gives no jobs.
func (r *runner) goJobs(proj project) []job {
	files := goFiles(proj.changed)
	dirs := packageDirs(proj.root, proj.changed)
	wholeModule := slices.Contains(proj.changed, "go.mod") || slices.Contains(proj.changed, "go.sum")

	if len(dirs) == 0 && !wholeModule {
		return nil
	}

	graph := &lazyGraph{r: r, root: proj.root}

	return []job{
		{run: func(ctx context.Context) []Result { return r.goCompile(ctx, proj, graph, dirs, wholeModule) }},
		{run: func(ctx context.Context) []Result { return r.lint(ctx, proj, graph, files) }},
	}
}

// lintBehindBuild drops a lint abstention behind a build or test failure,
// which is the reason lint could not type-check and is already reported.
func lintBehindBuild(compiled, linted []Result) []Result {
	if slices.ContainsFunc(compiled, func(res Result) bool { return res.Status == StatusFail }) &&
		slices.ContainsFunc(linted, func(res Result) bool { return res.Status == StatusAbstained }) {
		return nil
	}

	return linted
}

// lazyGraph lists the module's import graph the first time a job asks and
// hands every later caller the same answer.
type lazyGraph struct {
	r      *runner
	root   string
	goBin  string
	reason string
	graph  importGraph
	once   sync.Once
}

// get returns the go binary and the graph, or the reason there are none.
func (l *lazyGraph) get(ctx context.Context) (string, importGraph, string) {
	l.once.Do(func() {
		goBin, err := l.r.lookPath("go")
		if err != nil {
			l.reason = "go not on PATH"

			return
		}

		l.goBin = goBin
		l.graph, l.reason = loadGraph(ctx, goBin, l.root)
	})

	return l.goBin, l.graph, l.reason
}

// packageDirs returns the directories of the Go packages a change touches.
// A Go source file selects its own directory. Any other file selects the
// nearest directory above it that holds Go source, because a package's
// tests read its testdata and its source embeds the files beside it, and a
// change to either can break the package without touching a .go file. The
// walk stops at the root and at a nested module, and a file under a
// directory the go tool ignores selects nothing.
func packageDirs(root string, changed []string) []string {
	var dirs []string

	for _, file := range changed {
		dir, ok := packageDir(root, file)
		if ok && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

func packageDir(root, file string) (string, bool) {
	if file == "go.mod" || file == "go.sum" {
		return "", false
	}

	dir := path.Dir(file)
	segments := strings.Split(dir, "/")

	if at := slices.Index(segments, "testdata"); at >= 0 {
		// Test data belongs to the package that holds the directory,
		// whatever Go files it carries as fixtures.
		segments = segments[:at]
		dir = path.Join(segments...)
	} else if path.Ext(file) == ".go" {
		return dir, !slices.ContainsFunc(segments, skippedDir)
	}

	if dir == "" {
		dir = "."
	}

	if slices.ContainsFunc(segments, func(s string) bool { return skippedDir(s) || s == "node_modules" }) {
		return "", false
	}

	for {
		full := filepath.Join(root, filepath.FromSlash(dir))

		switch {
		case dir != "." && isFile(filepath.Join(full, "go.mod")):
			return "", false
		case holdsGo(full):
			return dir, true
		case dir == ".":
			return "", false
		}

		dir = path.Dir(dir)
	}
}

// holdsGo reports whether dir has a Go source file directly in it.
func holdsGo(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	return slices.ContainsFunc(entries, func(e os.DirEntry) bool {
		return !e.IsDir() && strings.HasSuffix(e.Name(), ".go")
	})
}

// goCompile produces the build and test results. Non-test code is compiled
// first because that is the fast way to the common failure. Test code is
// compiled by the test run itself, so a test binary is linked once: when
// that run reports packages that did not build, the failure belongs to
// build and test has nothing to add.
func (r *runner) goCompile(
	ctx context.Context, proj project, graph *lazyGraph, dirs []string, wholeModule bool,
) []Result {
	build := Result{Check: config.BuiltinBuild, Blocking: true}
	test := Result{Check: config.BuiltinTest, Blocking: true}
	wantBuild, wantTest := proj.cfg.Enabled(build.Check), proj.cfg.Enabled(test.Check)

	if !wantBuild && !wantTest {
		return nil
	}

	goBin, pkgs, reason := r.goScope(ctx, graph, dirs, wholeModule)
	if reason != "" {
		return enabled(wantBuild, build.abstain(reason), wantTest, test.abstain(reason))
	}

	if len(pkgs.all) == 0 {
		return nil
	}

	if wantBuild && len(pkgs.buildable) > 0 {
		inv := r.compileOnly(proj.root, goBin, pkgs.buildable)
		if build = classifyGo(proj.root, build, run(ctx, inv), evidence{again: inv}); build.Status != StatusPass {
			return []Result{build}
		}
	}

	plan := testPlan{pkgs: pkgs.all}
	if wantTest {
		plan = r.planTests(ctx, proj, pkgs.all)
	}

	if len(plan.pkgs) == 0 {
		return enabled(wantBuild, build.pass(), wantTest, test.pass())
	}

	return r.goTests(ctx, proj.root, goBin, plan, build, test, wantBuild, wantTest)
}

// compileOnly is the command that compiles pkgs and links nothing.
//
// `go build` is not it. Handed exactly one main package it writes the
// binary into the working directory, which here is the project root, and
// with `-o /dev/null` it still pays for a link whose output is thrown away.
// `go list -export` runs the compiler over each package for its export data
// and stops there, and prints the same diagnostics.
func (r *runner) compileOnly(root, goBin string, pkgs []string) invocation {
	return invocation{
		dir: root, name: goBin, timeout: r.commandTimeout(),
		args: append([]string{"list", "-export", "-f", quietFormat, "--"}, pkgs...),
	}
}

// goTests runs the planned tests, or only compiles the planned packages'
// tests when the project turned the test check off, and settles what build
// and test each say.
func (r *runner) goTests(
	ctx context.Context, root, goBin string, plan testPlan, build, test Result, wantBuild, wantTest bool,
) []Result {
	pkgs := plan.pkgs

	// The linkOnly arguments build the test binaries and run nothing,
	// which is also how a build failure found by the test run is put to
	// the baseline tree. A binary run with -run '^$' still runs TestMain,
	// which in some projects starts a database, so it is handed to true.
	linkOnly := []string{goTest, "-run", "^$"}
	if noop, err := r.lookPath("true"); err == nil {
		linkOnly = append(linkOnly, "-exec", noop)
	}

	args := []string{goTest, r.testTimeout()}
	if plan.run != "" {
		args = append(args, "-run", plan.run)
	}

	if !wantTest {
		linkOnly = append(linkOnly, "-vet=off")
		args = linkOnly
	}

	inv := invocation{dir: root, name: goBin, timeout: r.commandTimeout(), args: slices.Concat(args, pkgs)}
	out := run(ctx, inv)

	if wantBuild && (!wantTest || hasLine(out.output, buildVerdict.MatchString)) {
		inv.args = slices.Concat(linkOnly, pkgs)

		return []Result{classifyGo(root, build, out, evidence{again: inv})}
	}

	test = classifyGo(root, test, out, evidence{again: inv, tests: true})

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
// A non-empty reason means neither could be established. A change to go.mod
// or go.sum affects every package and needs no graph.
func (r *runner) goScope(
	ctx context.Context, graph *lazyGraph, dirs []string, wholeModule bool,
) (string, scope, string) {
	if wholeModule {
		goBin, err := r.lookPath("go")
		if err != nil {
			return "", scope{}, "go not on PATH"
		}

		return goBin, scope{all: []string{wholeModulePattern}, buildable: []string{wholeModulePattern}}, ""
	}

	goBin, g, reason := graph.get(ctx)
	if reason != "" {
		return "", scope{}, reason
	}

	return goBin, g.affected(dirs), ""
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
func classifyGo(root string, res Result, out outcome, ev evidence) Result {
	switch {
	case out.reason != "":
		return res.abstain(out.reason)
	case out.exit == 0:
		return res.pass()
	case hasLine(out.output, sourceFailure):
		ev.output = out.output
		res.evidence = &ev

		return res.fail(root, out.output)
	default:
		return res.abstain(oneLine(out.output))
	}
}

func sourceFailure(line string) bool {
	return goDiagnostic.MatchString(line) || testVerdict.MatchString(line) ||
		strings.HasPrefix(line, "--- FAIL") || strings.HasPrefix(line, "panic:") ||
		strings.Contains(line, "import cycle not allowed") || strings.HasPrefix(line, "found packages ")
}
