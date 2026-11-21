package reduce

import (
	"regexp"
	"strings"
)

// diagnostic is a compiler or vet line: a path, a line number, an optional
// column, then the message.
var diagnostic = regexp.MustCompile(`^\S+\.go:\d+(:\d+)?: `)

// goBuild keeps the diagnostics a failed `go build`, `go vet`, or
// non-compiling `go test` run emits, once each. The package header lines it
// groups them under repeat the path the diagnostic already carries, the same
// error reported from twenty call sites is one thing to fix, and the packages
// that were fine are not what the reader came for.
var goBuild = reducer{
	detect: hasDiagnostic,
	keep:   keepDiagnostics,
}

func hasDiagnostic(lines []string) bool {
	for _, line := range lines {
		if diagnostic.MatchString(strings.TrimLeft(line, " \t")) {
			return true
		}
	}

	return false
}

// keepDiagnostics drops the verdicts of packages that passed only from a run
// that says a package failed to build. Without that marker the text may be a
// test's own output that happens to carry a diagnostic's shape, and its
// verdict lines are the answer.
func keepDiagnostics(lines []string) []string {
	out := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	failed := countMatching(lines, func(l string) bool { return strings.Contains(l, "[build failed]") }) > 0

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")

		switch {
		case strings.HasPrefix(trimmed, "#"), trimmed == "":
		case failed && (trimmed == "FAIL" || packageVerdict.MatchString(line)):
		case seen[trimmed]:
		default:
			seen[trimmed] = true

			out = append(out, line)
		}
	}

	return out
}
