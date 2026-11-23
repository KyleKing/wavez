// Package reduce shrinks tool output to the lines a reader acts on, keeping
// what names a failure and dropping what only says the run happened.
package reduce

import (
	"fmt"
	"strings"
)

// reducer recognizes one shape of output and keeps the lines that shape puts
// the answer on. Detect sees every line, so a reducer claims output on a
// marker that only its own shape produces. Keep also returns how many lines
// of diagnostics it left out to stay short, as opposed to the lines that
// were noise by the shape's own definition.
type reducer struct {
	detect func(lines []string) bool
	keep   func(lines []string) ([]string, int)
}

// reducers are tried in order and the first to claim the output wins. A test
// run goes first because an assertion message has a compiler diagnostic's
// shape, while a build failure inside `go test` prints no test markers at all
// and so falls through to the build reducer on its own. Grouping sits ahead of
// the build reducer and declines anything it cannot halve, so a short list of
// distinct diagnostics still arrives one per line.
var reducers = []reducer{goTest, findings, goBuild}

// Output reduces s, returning it unchanged when nothing recognizes its shape
// and nothing repeats. Reducing twice returns the same text, so output that
// has already passed through survives a second pass intact. Diagnostics a
// cap left out are counted in one closing line.
func Output(s string) string {
	if s == "" {
		return s
	}

	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")

	kept, cut := reduceLines(lines)
	if cut == 0 && len(kept) == len(lines) {
		return s
	}

	return strings.Join(withTrailer(kept, cut), "\n")
}

// Failure is what a failed command printed, cut down for a reader who has
// to fix it: paths under root made relative, the output reduced, and the
// result bounded to limit bytes. Lines are dropped whole, never cut in the
// middle of the text, the lines that name a failing test or package are
// always kept, and one closing line says how many lines are not shown. An
// empty root leaves paths alone and a limit of zero or less is no bound.
func Failure(output, root string, limit int) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}

	if root != "" {
		output = strings.ReplaceAll(output, strings.TrimRight(root, "/")+"/", "")
	}

	kept, cut := reduceLines(strings.Split(output, "\n"))
	kept, more := boundLines(kept, limit)

	return strings.Join(withTrailer(kept, cut+more), "\n")
}

func reduceLines(lines []string) ([]string, int) {
	for _, r := range reducers {
		if r.detect(lines) {
			return r.keep(lines)
		}
	}

	return collapseRuns(lines), 0
}

// trailerSuffix ends the one line that counts what a reduction left out.
const trailerSuffix = " not shown)"

func withTrailer(lines []string, cut int) []string {
	if cut == 0 {
		return lines
	}

	return append(lines, fmt.Sprintf("(%s%s", plural(cut, "more line"), trailerSuffix))
}

// collapseRuns replaces a run of identical adjacent lines with the line and a
// count. A loop printing the same warning per file says as much once.
func collapseRuns(lines []string) []string {
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}

		out = append(out, lines[i])
		if n := j - i; n > 1 {
			out = append(out, fmt.Sprintf("    ... the line above repeats %d times ...", n))
		}

		i = j
	}

	return out
}
