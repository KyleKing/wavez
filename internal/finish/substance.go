package finish

import (
	"strings"
)

// commentPrefixes open a comment in the languages this project gates. A
// prefix list is a bound rather than a parser: it is allowed to miss a
// comment shape, which makes the check say nothing, and it must never call
// real code a comment, which would fail a run that did the work.
var commentPrefixes = []string{"//", "/*", "*/", "*", "#"}

// ChangeHasSubstance reports a run whose whole diff is comments and blank
// lines.
//
// It exists because a run can satisfy every other bound by doing nothing.
// Measured on `h6`: a run added the line `// Ensure we truncate on a
// character boundary` above the code it was asked to rewrite, left the code
// alone, and reported complete. Every gate passed, because nothing was
// broken; the change set touched the file the task named, because it did;
// the closing answer named only things that exist, because it described
// work it had not done; and the changed line was covered by a test, because
// it sat inside a tested function.
//
// An empty diff says nothing rather than failing: a run that changed no
// file is what ChangeSetMatchesTask reports, and two findings for one fact
// is noise.
func ChangeHasSubstance(diff string) Report {
	var code, comments int

	for _, line := range strings.Split(diff, "\n") {
		if !isEdit(line) {
			continue
		}

		if isComment(line[1:]) {
			comments++

			continue
		}

		code++
	}

	if code > 0 || comments == 0 {
		return Report{}
	}

	return Report{Findings: []Finding{{
		Check:  "the run changed nothing but comments",
		Detail: "every added and removed line is a comment or blank",
	}}}
}

// isEdit reports a unified-diff body line, excluding the +++/--- file
// headers that share its first character.
func isEdit(line string) bool {
	if line == "" {
		return false
	}

	if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
		return false
	}

	return line[0] == '+' || line[0] == '-'
}

func isComment(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}

	for _, prefix := range commentPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return false
}
