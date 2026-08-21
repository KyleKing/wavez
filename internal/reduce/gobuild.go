package reduce

import (
	"regexp"
	"strings"
)

// diagnostic is a compiler or vet line: a path, a line number, an optional
// column, then the message.
var diagnostic = regexp.MustCompile(`^\S+\.go:\d+(:\d+)?: `)

// goBuild keeps the diagnostics a failed `go build` or `go vet` run emits,
// once each. The package header lines it groups them under repeat the path
// the diagnostic already carries, and the same error reported from twenty
// call sites is one thing to fix.
var goBuild = reducer{
	name:   "go build",
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

func keepDiagnostics(lines []string) []string {
	out := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")

		switch {
		case strings.HasPrefix(trimmed, "#"), trimmed == "":
		case seen[trimmed]:
		default:
			seen[trimmed] = true

			out = append(out, line)
		}
	}

	return out
}
