package reduce

import (
	"fmt"
	"regexp"
	"strings"
)

// goTest keeps the lines a failing `go test` run puts the answer on: the
// failing test names, whatever they printed, and the per-package verdicts. A
// passing subtest is reported by the package's own ok line, so `=== RUN` and
// `--- PASS` are the bulk of a verbose run and say nothing the summary does
// not.
var goTest = reducer{
	name:   "go test",
	detect: isGoTest,
	keep:   keepGoTest,
}

// goTestNoise are the verbose markers a run emits per test regardless of
// outcome.
var goTestNoise = []string{"=== RUN", "=== PAUSE", "=== CONT", "=== NAME", "--- PASS", "--- SKIP"}

// packageVerdict matches the per-package lines `go test ./...` prints for a
// package that gave no trouble.
var packageVerdict = regexp.MustCompile(`^(ok {2}\t|\? {3}\t)`)

// minCollapse is how many quiet package verdicts it takes before they read as
// noise. Unlike a --- PASS line, `ok pkg 0.2s` names a package and its time,
// so a run over one or two packages keeps them and only a sweep collapses.
const minCollapse = 4

// isGoTest claims a verbose run by its per-test markers and a plain sweep by
// the per-package verdicts it prints instead, since a sweep over this module
// is thirty lines of which the failures are three.
func isGoTest(lines []string) bool {
	return hasPrefixLine("=== RUN", "--- PASS", "--- FAIL", "--- SKIP")(lines) ||
		countMatching(lines, packageVerdict.MatchString) >= minCollapse
}

func keepGoTest(lines []string) []string {
	out := make([]string, 0, len(lines))
	passed, quiet := 0, 0
	collapse := countMatching(lines, packageVerdict.MatchString) >= minCollapse
	// A failing test's output is the indented block under its --- FAIL, and
	// go prints that block before the marker, so a kept marker pulls the
	// indented run above it back in.
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")

		switch {
		case strings.HasPrefix(trimmed, "--- FAIL"), strings.HasPrefix(trimmed, "=== FAIL"):
			out = append(out, indentedRunBefore(lines, i, out)...)
			out = append(out, line)
		case hasAnyPrefix(trimmed, goTestNoise):
			if strings.HasPrefix(trimmed, "--- PASS") {
				passed++
			}
		case collapse && packageVerdict.MatchString(line):
			quiet++
		case trimmed == "":
		default:
			out = append(out, line)
		}
	}

	if passed > 0 {
		out = append(out, fmt.Sprintf("(%s passed)", plural(passed, "subtest")))
	}

	if quiet > 0 {
		out = append(out, fmt.Sprintf("(%s ok or without tests)", plural(quiet, "package")))
	}

	return out
}

// indentedRunBefore returns the indented body immediately above i that out
// does not already end with, which is what the failing test printed. The walk
// stops at the nearest marker, since a subtest's own verdict line ends the
// block above it and is not part of it.
func indentedRunBefore(lines []string, i int, out []string) []string {
	start := i
	for start > 0 && isBody(lines[start-1]) {
		start--
	}

	body := lines[start:i]
	for len(body) > 0 && len(out) > 0 && out[len(out)-1] == body[0] {
		body = body[1:]
	}

	return body
}

// isBody reports whether s is output a test printed rather than a marker the
// runner emitted about one.
func isBody(s string) bool {
	trimmed := strings.TrimLeft(s, " \t")
	if trimmed == "" || len(trimmed) == len(s) {
		return false
	}

	return !hasAnyPrefix(trimmed, goTestNoise) && !strings.HasPrefix(trimmed, "--- FAIL") &&
		!strings.HasPrefix(trimmed, "=== FAIL")
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
