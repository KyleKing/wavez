package reduce

import (
	"fmt"
	"regexp"
	"strings"
)

// goTest keeps the lines a failing `go test` run puts the answer on: the
// failing test names, the first lines each printed, a panic's message with
// its frames inside the project, and the per-package verdicts. A passing
// subtest is reported by the package's own ok line, so `=== RUN` and
// `--- PASS` are the bulk of a verbose run and say nothing the summary does
// not.
var goTest = reducer{
	detect: isGoTest,
	keep:   keepGoTest,
}

// goTestNoise are the verbose markers a run emits per test regardless of
// outcome.
var goTestNoise = []string{"=== RUN", "=== PAUSE", "=== CONT", "=== NAME", "--- PASS", "--- SKIP"}

// packageVerdict matches the per-package lines `go test ./...` prints for a
// package that gave no trouble.
var packageVerdict = regexp.MustCompile(`^(ok {2}\t|\? {3}\t)`)

// minCollapse is how many quiet package verdicts it takes before a passing
// run's read as noise. Unlike a --- PASS line, `ok pkg 0.2s` names a package
// and its time, so a passing run over one or two packages keeps them. A
// failing run collapses them whatever their number: its reader wants the
// failure.
const minCollapse = 4

// testBodyLines is how many lines of what a test printed are kept after
// each marker. The first lines of a failing test carry the assertion; the
// thousandth says what the first did.
const testBodyLines = 5

// failVerdict matches the per-package line of a package that failed.
var failVerdict = regexp.MustCompile(`^FAIL\t`)

// isGoTest claims a verbose run by its per-test markers and a plain sweep by
// the per-package verdicts it prints instead, since a sweep over this module
// is thirty lines of which the failures are three. A run that died in a
// panic with nothing but its package verdict is still a test run.
func isGoTest(lines []string) bool {
	return hasPrefixLine("=== RUN", "--- PASS", "--- FAIL", "--- SKIP")(lines) ||
		countMatching(lines, packageVerdict.MatchString) >= minCollapse ||
		(countMatching(lines, failVerdict.MatchString) > 0 && countMatching(lines, isPanic) > 0)
}

// keepGoTest returns the kept lines and how many lines of test output its
// caps left out. Every failure marker and every failing package's verdict
// is kept whatever the caps say.
func keepGoTest(lines []string) ([]string, int) {
	out := make([]string, 0, len(lines))
	passed, quiet, cut, body := 0, 0, 0, 0
	failing := countMatching(lines, failVerdict.MatchString) > 0
	collapse := failing || countMatching(lines, packageVerdict.MatchString) >= minCollapse

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")

		switch {
		case isFailMarker(trimmed), failVerdict.MatchString(line):
			out = append(out, line)
			body = 0
		case hasAnyPrefix(trimmed, goTestNoise):
			if strings.HasPrefix(trimmed, "--- PASS") {
				passed++
			}

			body = 0
		case collapse && packageVerdict.MatchString(line):
			quiet++
		case trimmed == "", collapse && line == bareFail:
		case isPanic(line):
			end := panicEnd(lines, i)
			kept := collapsePanic(lines[i:end])
			out = append(out, kept...)
			cut += end - i - len(kept)
			i, body = end-1, 0
		case body >= testBodyLines:
			cut++
		default:
			out = append(out, line)
			body++
		}
	}

	return counted(out, passed, quiet, failing), cut
}

// counted closes the kept lines with how many tests and packages passed
// without being listed.
func counted(out []string, passed, quiet int, failing bool) []string {
	if passed > 0 {
		out = append(out, fmt.Sprintf("(%s passed)", plural(passed, "subtest")))
	}

	// A failing run's reader is not asking how many packages were fine.
	if quiet > 0 && !failing {
		out = append(out, fmt.Sprintf("(%s ok or without tests)", plural(quiet, "package")))
	}

	return out
}

func isFailMarker(trimmed string) bool {
	return strings.HasPrefix(trimmed, "--- FAIL") || strings.HasPrefix(trimmed, "=== FAIL")
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}

	return false
}

// hasPrefixLine builds a detect that claims output carrying any of markers at
// the start of a line, ignoring indentation.
func hasPrefixLine(markers ...string) func([]string) bool {
	return func(lines []string) bool {
		for _, line := range lines {
			if hasAnyPrefix(strings.TrimLeft(line, " \t"), markers) {
				return true
			}
		}

		return false
	}
}

func countMatching(lines []string, match func(string) bool) int {
	n := 0

	for _, line := range lines {
		if match(line) {
			n++
		}
	}

	return n
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}
