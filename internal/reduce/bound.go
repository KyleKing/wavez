package reduce

import (
	"strings"
	"unicode/utf8"
)

const (
	// The headShare is the part of a bounded window given to the head. A compiler
	// puts the first cause at the top and a test run puts the verdict at the
	// bottom, so both ends are kept and the head gets the larger half.
	headShare  = 3
	shareParts = 5
	// The maxLine is the longest a kept line may be. A longer one is a dump, and
	// what names the failure is at its start.
	maxLine = 400
)

// boundLines returns the lines that fit in limit bytes and how many were
// left out. The lines that name a failing test or package go in first, then
// lines from the top and from the bottom. A line is kept or dropped whole;
// one longer than maxLine is shortened at its end and says so.
func boundLines(lines []string, limit int) ([]string, int) {
	total := 0

	for i, line := range lines {
		lines[i] = shorten(line)
		total += len(lines[i]) + 1
	}

	if limit <= 0 || total <= limit {
		return lines, 0
	}

	keep := make([]bool, len(lines))
	used := 0
	take := func(i, budget int) bool {
		if keep[i] {
			return true
		}

		if used+len(lines[i])+1 > budget {
			return false
		}

		keep[i] = true
		used += len(lines[i]) + 1

		return true
	}

	for i, line := range lines {
		if pinned(line) {
			take(i, limit)
		}
	}

	head := used + (limit-used)*headShare/shareParts
	for i := 0; i < len(lines) && take(i, head); i++ {
		continue
	}

	for i := len(lines) - 1; i >= 0 && take(i, limit); i-- {
		continue
	}

	out := make([]string, 0, len(lines))

	for i, line := range lines {
		if keep[i] {
			out = append(out, line)
		}
	}

	return out, len(lines) - len(out)
}

// pinned reports a line a bound must not drop: it names a failing test or a
// failing package.
func pinned(line string) bool {
	return isFailMarker(strings.TrimLeft(line, " \t")) || failVerdict.MatchString(line)
}

func shorten(line string) string {
	if len(line) <= maxLine {
		return line
	}

	cut := maxLine
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}

	return line[:cut] + " [line shortened]"
}
