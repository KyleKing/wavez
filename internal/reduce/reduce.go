// Package reduce shrinks tool output to the lines a reader acts on, keeping
// what names a failure and dropping what only says the run happened.
package reduce

import (
	"fmt"
	"strings"
)

// Result is reduced output and what it cost. Dropped counts source lines the
// text no longer carries, so a caller can record the saving without diffing.
// Complete marks text that accounts for every input line rather than sampling
// them, which is what makes a further head-and-tail window unnecessary and
// destructive.
type Result struct {
	Text     string
	Kept     int
	Dropped  int
	Complete bool
}

// reducer recognizes one shape of output and keeps the lines that shape puts
// the answer on. Detect sees every line, so a reducer claims output on a
// marker that only its own shape produces.
type reducer struct {
	detect func(lines []string) bool
	keep   func(lines []string) []string
	name   string
	// complete says the kept text accounts for every input line and is bounded
	// by its own shape, so a caller may skip the windowing it applies to output
	// it cannot reason about.
	complete bool
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
// has already passed through survives a second pass intact.
func Output(s string) Result {
	if s == "" {
		return Result{Text: s}
	}

	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")

	kept, name, complete := collapseRuns(lines), "repeats", false

	for _, r := range reducers {
		if r.detect(lines) {
			kept, name, complete = r.keep(lines), r.name, r.complete

			break
		}
	}

	dropped := len(lines) - len(kept)
	if dropped <= 0 {
		return Result{Text: s, Kept: len(lines), Complete: complete}
	}

	kept = append(kept, fmt.Sprintf("... [%d of %d lines dropped as %s noise] ...", dropped, len(lines), name))

	return Result{Text: strings.Join(kept, "\n"), Kept: len(kept), Dropped: dropped, Complete: complete}
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
