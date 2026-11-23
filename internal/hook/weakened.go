package hook

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	skipCall = regexp.MustCompile(`\.Skip(f|Now)?\(`)
	testFunc = regexp.MustCompile(`^func (Test\w*)\(`)
)

// maxWeakened bounds how many findings the person's line names.
const maxWeakened = 4

// weakening collects what a diff of test files shows.
type weakening struct {
	// removed maps a test function that went to the file it went from.
	removed map[string]string
	added   map[string]bool
	// file is the one the lines being read belong to.
	file  string
	skips []string
}

// header reads a diff's file line, and reports whether line was one. A
// deleted file has no new name, so its old one stands.
func (w *weakening) header(line string) bool {
	for _, lead := range []string{"--- a/", "+++ b/", "--- ", "+++ "} {
		if name, ok := strings.CutPrefix(line, lead); ok {
			if name != "/dev/null" {
				w.file = name
			}

			return true
		}
	}

	return false
}

func (w *weakening) read(line string) {
	body, wasAdded := strings.CutPrefix(line, "+")

	if !wasAdded {
		if body, ok := strings.CutPrefix(line, "-"); ok {
			if match := testFunc.FindStringSubmatch(body); len(match) > 1 {
				w.removed[match[1]] = w.file
			}
		}

		return
	}

	if skipCall.MatchString(body) && !slices.Contains(w.skips, w.file) {
		w.skips = append(w.skips, w.file)
	}

	if match := testFunc.FindStringSubmatch(body); len(match) > 1 {
		w.added[match[1]] = true
	}
}

// weakened reads a diff of Go test files and names the ways the session
// made the tests ask for less: a skip it added, and a test function it
// removed and did not put back anywhere in the diff. It is for the person
// to look at, since either can be right. An agent that meets a failing gate
// can also get past it this way, and the gate cannot tell which it was.
func weakened(diff string) []string {
	found := weakening{removed: map[string]string{}, added: map[string]bool{}}

	for line := range strings.SplitSeq(diff, "\n") {
		if !found.header(line) {
			found.read(line)
		}
	}

	out := make([]string, 0, len(found.skips)+len(found.removed))
	for _, file := range found.skips {
		out = append(out, "skip added in "+file)
	}

	var gone []string

	for name, from := range found.removed {
		if !found.added[name] {
			gone = append(gone, fmt.Sprintf("%s removed from %s", name, from))
		}
	}

	slices.Sort(gone)

	return append(out, gone...)
}

// review is the findings as one part of the end-of-turn line.
func review(found []string) string {
	if len(found) == 0 {
		return ""
	}

	more := ""
	if len(found) > maxWeakened {
		more = fmt.Sprintf(", and %d more", len(found)-maxWeakened)
		found = found[:maxWeakened]
	}

	return "review: " + strings.Join(found, ", ") + more
}
