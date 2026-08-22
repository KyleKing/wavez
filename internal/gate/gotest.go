package gate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// goTestEvent mirrors the wire format `go test -json` itself defines
// (cmd/internal/test2json), so its field names and tags are fixed by that
// protocol, not by this package's own JSON conventions.
//
//nolint:tagliatelle // field names and tags match go test -json's own wire format exactly
type goTestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Output  string    `json:"Output"`
	// ImportPath is set instead of Package on the build-output/build-fail
	// events a compile error produces, before go test ever gets to run
	// anything in the package.
	ImportPath string `json:"ImportPath"`
	// FailedBuild is set only on the package-level fail event a compile
	// error produces, and matches the build-output events' ImportPath, which
	// is what tells a build failure apart from the package-level fail event
	// go test also emits to summarize a normal in-package test failure.
	FailedBuild string `json:"FailedBuild"`
}

// FailedTest is one test `go test -json` reported failed, with its raw
// output lines for TrimFailure to filter.
type FailedTest struct {
	Name    string
	Package string
	Output  []string
}

// GoTestSummary is what one `go test -json` run reported, before trimming.
// BuildFailed is set by a package-level fail event carrying no test name,
// which is how a compile error surfaces in the stream rather than as a
// normal test failure.
type GoTestSummary struct {
	FailedTests []FailedTest
	PassedTests []string
	// TestlessPackages are packages go test skipped whole because they hold
	// no test file. They are why a run examining nothing is not always a
	// drifted selection.
	TestlessPackages []string
	BuildFailed      bool
	Pass             bool
}

const (
	scannerInitialBuf = 64 * 1024
	scannerMaxBuf     = 4 * 1024 * 1024

	// The go-test resource key names both GoTestGate's gate name and the
	// build-cache resource key it shares with BuildGate.
	goTestResource = "go-test"

	// The contextLines bound caps the untrimmed output a failure carries
	// when no line named a changed file.
	contextLines = 6
)

// ParseGoTestJSON reads a `go test -json` event stream and summarizes it.
func ParseGoTestJSON(r io.Reader) (GoTestSummary, error) {
	type key struct{ pkg, test string }

	output := make(map[key][]string)
	buildOutput := make(map[string][]string) // keyed by ImportPath

	var summary GoTestSummary

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)

	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ev goTestEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return GoTestSummary{}, fmt.Errorf("parsing go test -json event: %w", err)
		}

		k := key{ev.Package, ev.Test}

		switch ev.Action {
		case "output":
			output[k] = append(output[k], ev.Output)
		case "build-output":
			buildOutput[ev.ImportPath] = append(buildOutput[ev.ImportPath], ev.Output)
		case "skip":
			if ev.Test == "" {
				summary.TestlessPackages = append(summary.TestlessPackages, ev.Package)
			}
		case "pass":
			if ev.Test != "" {
				summary.PassedTests = append(summary.PassedTests, ev.Package+"."+ev.Test)
			}
		case "fail":
			switch {
			case ev.Test != "":
				summary.FailedTests = append(summary.FailedTests, FailedTest{
					Name:    ev.Test,
					Package: ev.Package,
					Output:  output[k],
				})
			case ev.FailedBuild != "":
				summary.BuildFailed = true
				summary.FailedTests = append(summary.FailedTests, FailedTest{
					Package: ev.Package,
					Output:  append(append([]string(nil), buildOutput[ev.FailedBuild]...), output[k]...),
				})
			}
			// A package-level fail event with neither a test name nor
			// FailedBuild is go test's own rollup once an in-package test
			// already failed individually; it carries nothing new.
		}
	}

	if err := sc.Err(); err != nil {
		return GoTestSummary{}, fmt.Errorf("reading go test -json output: %w", err)
	}

	summary.Pass = !summary.BuildFailed && len(summary.FailedTests) == 0

	return summary, nil
}

var fileLineRe = regexp.MustCompile(`([\w./-]+\.go):(\d+)`)

// TrimFailure drops every output line of failure that does not reference
// one of changedFiles. Frames are matched by base name because both the
// testing package's "file.go:12:" prefix and a panic trace's short source
// path omit the full repo-relative path.
func TrimFailure(failure FailedTest, changedFiles []string) TrimmedFailure {
	changedBase := make(map[string]struct{}, len(changedFiles))
	for _, f := range changedFiles {
		changedBase[filepath.Base(f)] = struct{}{}
	}

	var frames []string

	for _, line := range failure.Output {
		for _, m := range fileLineRe.FindAllStringSubmatch(line, -1) {
			if _, ok := changedBase[filepath.Base(m[1])]; ok {
				frames = append(frames, strings.TrimRight(line, "\n"))

				break
			}
		}
	}

	if len(frames) == 0 {
		return TrimmedFailure{
			Test:    failure.Name,
			Package: failure.Package,
			Context: outputHead(failure.Output),
		}
	}

	return TrimmedFailure{Test: failure.Name, Package: failure.Package, Frames: frames}
}

// outputHead is the first few meaningful lines of an untrimmed failure. It
// is bounded because the point is to say what kind of failure this is, not
// to hand back the whole log.
func outputHead(lines []string) []string {
	out := make([]string, 0, contextLines)

	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}

		out = append(out, trimmed)

		if len(out) == contextLines {
			break
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// GoTestGate runs the selected Go tests via `go test -json` and reports
// pass/fail, trimming any failure to the frames that touch changed files.
type GoTestGate struct {
	repoRoot string
}

// NewGoTestGate builds a GoTestGate rooted at repoRoot.
func NewGoTestGate(repoRoot string) *GoTestGate {
	return &GoTestGate{repoRoot: repoRoot}
}

// Name identifies this gate in the gate log.
func (*GoTestGate) Name() string { return goTestResource }

// Resources reports the exclusive resource this gate holds while running.
func (*GoTestGate) Resources() []string { return []string{goTestResource} }

// Run executes the tests or packages rc.Selection names.
func (g *GoTestGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	changedGo := len(goFiles(rc.Changes))
	if changedGo == 0 {
		return Abstained(g.Name(), rc.Selection.Level, "no changed Go file"), nil
	}

	args := buildTestArgs(rc.Selection)
	if len(args) == 0 {
		return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"selection produced no tests or packages for %d changed Go file(s), so nothing was run",
			changedGo)), nil
	}

	//nolint:gosec // args are a fixed subset of Selection's own test/package names
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "-json"}, args...)...)
	cmd.Dir = g.repoRoot

	// go test's own process exit status is redundant with the parsed
	// summary below, and it exits non-zero on any test failure regardless.
	out, _ := cmd.Output() //nolint:errcheck // status carried by the parsed summary, not this call's error

	summary, err := ParseGoTestJSON(bytes.NewReader(out))
	if err != nil {
		return Result{}, fmt.Errorf("go test gate: %w", err)
	}

	ran := len(summary.PassedTests) + len(summary.FailedTests)

	// `go test -run <pattern>` exits 0 when the pattern matches nothing, so
	// a selection whose regex has drifted from the test names reports a
	// clean pass having executed no test at all. That is the whole reason
	// Examined exists: a green gate that ran zero tests is the failure this
	// catches, and it is invisible from Pass alone.
	if ran == 0 {
		// A package go test skipped whole holds no test file, which is a
		// change set with no work for this gate rather than a selection that
		// drifted off the tests that do exist. Reporting it as a failure
		// tells a run in a test-less package to write a test on every turn,
		// which is the demand the abstention rule exists to avoid.
		if len(summary.TestlessPackages) > 0 && !summary.BuildFailed {
			return Abstained(g.Name(), rc.Selection.Level, fmt.Sprintf(
				"%d selected package(s) hold no test file", len(summary.TestlessPackages))), nil
		}

		return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"go test ran 0 tests for %d changed Go file(s) at %s level; the selection matched no test",
			changedGo, rc.Selection.Level)), nil
	}

	result := Result{
		Gate:     g.Name(),
		Level:    rc.Selection.Level,
		Examined: ran,
		Pass:     summary.Pass,
	}

	changed := changedPaths(rc.Changes)
	for _, f := range summary.FailedTests {
		result.Failures = append(result.Failures, TrimFailure(f, changed))
	}

	return result, nil
}

func changedPaths(changes []tool.Change) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.Path
	}

	return out
}

// buildTestArgs turns a Selection into `go test` arguments: a -run regex
// scoped to the involved packages at LevelLine, or the bare package list at
// LevelImporter and LevelPackage.
func buildTestArgs(sel Selection) []string {
	switch sel.Level {
	case LevelLine:
		if len(sel.Tests) == 0 {
			return nil
		}

		return append([]string{"-run", testNamesToRegex(sel.Tests)}, testPackages(sel.Tests)...)
	case LevelImporter, LevelPackage:
		if len(sel.Packages) == 0 {
			return nil
		}

		return append([]string(nil), sel.Packages...)
	default:
		return nil
	}
}

func testNamesToRegex(testIDs []string) string {
	seen := make(map[string]struct{})

	var names []string

	for _, id := range testIDs {
		name := testName(id)
		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}

		names = append(names, name)
	}

	sort.Strings(names)

	return "^(" + strings.Join(names, "|") + ")$"
}

func testPackages(testIDs []string) []string {
	seen := make(map[string]struct{})

	var pkgs []string

	for _, id := range testIDs {
		pkg := testPackage(id)
		if _, ok := seen[pkg]; ok {
			continue
		}

		seen[pkg] = struct{}{}

		pkgs = append(pkgs, pkg)
	}

	sort.Strings(pkgs)

	return pkgs
}

func testPackage(id string) string {
	idx := strings.LastIndex(id, ".")
	if idx < 0 {
		return id
	}

	return id[:idx]
}

func testName(id string) string {
	idx := strings.LastIndex(id, ".")
	if idx < 0 {
		return id
	}

	return id[idx+1:]
}
