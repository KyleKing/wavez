package reduce

import (
	"regexp"
	"slices"
	"strings"
)

var (
	// The failedTest pattern matches the marker go test prints for a failing test or
	// subtest and captures its full name.
	failedTest = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	// The failedPackage pattern matches a failing package's verdict and captures its
	// import path. The path ends at a tab when the tests ran and at a space
	// when the package never got that far.
	failedPackage = regexp.MustCompile(`^FAIL\t(\S+)`)
	// The notBuilt pattern matches the bracketed note go test puts on a package whose
	// test binary did not compile or whose dependencies did not load.
	notBuilt = regexp.MustCompile(`^FAIL\t\S+ \[(build|setup) failed\]`)
)

// bareFail is the line go test prints under a package's failing tests, and
// once more to close a run in which any package failed.
const bareFail = "FAIL"

// PackageFailure is one package a `go test` run reported as failed.
type PackageFailure struct {
	// Package is the import path on the package's FAIL line.
	Package string
	// Tests holds the full names of the tests and subtests that failed, in
	// the order they were reported. It is empty for a package that failed
	// without naming a test, such as one whose TestMain exited.
	Tests []string
	// NotBuilt is true when the package's tests never ran because it did
	// not compile.
	NotBuilt bool
}

// GoTestFailures reads the output of a `go test` run and returns the
// packages that failed, each with the tests it named. A failure go printed
// with no package verdict after it, as a run cut short does, is not
// returned: nothing says which package it belongs to.
func GoTestFailures(output string) []PackageFailure {
	lines := strings.Split(output, "\n")

	var out []PackageFailure

	for _, sec := range testSections(lines) {
		if !sec.failed {
			continue
		}

		failure := PackageFailure{Package: sec.pkg, NotBuilt: sec.notBuilt}
		for _, block := range sec.blocks {
			failure.Tests = append(failure.Tests, block.names...)
		}

		out = append(out, failure)
	}

	return out
}

// WithoutGoTests returns the output of a `go test` run with the failures
// drop names removed. A top-level test's block goes when drop is true of it
// and of every failing subtest under it; a package's whole section, verdict
// included, goes when every failure it named did. A package that failed
// without naming a test is never removed.
func WithoutGoTests(output string, drop func(pkg, test string) bool) string {
	lines := strings.Split(output, "\n")
	gone := make([]bool, len(lines))

	for _, sec := range testSections(lines) {
		if !sec.failed {
			continue
		}

		kept := 0

		for _, block := range sec.blocks {
			if !slices.ContainsFunc(block.names, func(name string) bool { return !drop(sec.pkg, name) }) {
				mark(gone, block.start, block.end)

				continue
			}

			kept++
		}

		if kept == 0 && len(sec.blocks) > 0 && !sec.notBuilt {
			mark(gone, sec.start, sec.end)
		}
	}

	out := make([]string, 0, len(lines))

	for i, line := range lines {
		if !gone[i] {
			out = append(out, line)
		}
	}

	// A bare FAIL closes a run in which some package failed. With every
	// failing section gone it has nothing left to close.
	if !slices.ContainsFunc(out, failedPackage.MatchString) {
		out = slices.DeleteFunc(out, func(line string) bool { return line == bareFail })
	}

	return strings.Join(out, "\n")
}

func mark(gone []bool, start, end int) {
	for i := start; i < end; i++ {
		gone[i] = true
	}
}

// testSection is the lines one package's run printed, ending with its
// verdict. The start and end are a half-open range of line indexes.
type testSection struct {
	pkg      string
	blocks   []testBlock
	start    int
	end      int
	failed   bool
	notBuilt bool
}

// testBlock is one failure marker and the indented lines under it, which is
// how a non-verbose run prints a failing top-level test with its subtests.
type testBlock struct {
	names []string
	start int
	end   int
}

// testSections splits a run at its per-package verdicts. Lines after the
// last verdict belong to no section.
func testSections(lines []string) []testSection {
	var (
		out   []testSection
		start int
	)

	for i, line := range lines {
		m := failedPackage.FindStringSubmatch(line)
		if m == nil && !packageVerdict.MatchString(line) {
			continue
		}

		sec := testSection{start: start, end: i + 1}
		if m != nil {
			sec.pkg, sec.failed, sec.notBuilt = m[1], true, notBuilt.MatchString(line)
			sec.blocks = testBlocks(lines, start, i)
		}

		out = append(out, sec)
		start = i + 1
	}

	return out
}

// testBlocks finds the failure blocks between two line indexes. A marker
// that is itself indented with no block open, as a verbose run prints a
// subtest ahead of its parent, is a block of its own.
func testBlocks(lines []string, start, end int) []testBlock {
	var out []testBlock

	for i := start; i < end; i++ {
		m := failedTest.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}

		block := testBlock{start: i, end: i + 1, names: []string{m[1]}}

		for ; block.end < end && indented(lines[block.end]); block.end++ {
			if sub := failedTest.FindStringSubmatch(lines[block.end]); sub != nil {
				block.names = append(block.names, sub[1])
			}
		}

		// The package's own bare FAIL is unindented, so it stays outside.
		out = append(out, block)
		i = block.end - 1
	}

	return out
}

func indented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}
