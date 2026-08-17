package vcs

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// hunkHeader matches a unified-diff hunk header, capturing the post-image
// start line and length: `@@ -12,3 +14,5 @@`. The length is optional, and
// absent means one line.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// fileHeader matches the post-image path of a git-format diff, which is
// what jj's --git output produces.
var fileHeader = regexp.MustCompile(`^\+{3} b/(.+)$`)

// ChangedRanges maps each file in a git-format unified diff to the line
// ranges the change produced, in post-image coordinates. Ranges are what a
// gate scopes itself to, so they must name lines in the tree as it is now
// rather than as it was.
//
// A hunk with a zero post-image length is a pure deletion and contributes
// no range, because there is no line left to check.
func ChangedRanges(diff string) map[string][]tool.LineRange {
	out := map[string][]tool.LineRange{}

	var current string

	for _, line := range strings.Split(diff, "\n") {
		if m := fileHeader.FindStringSubmatch(line); m != nil {
			current = m[1]

			continue
		}

		m := hunkHeader.FindStringSubmatch(line)
		if m == nil || current == "" {
			continue
		}

		start, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}

		length := 1
		if m[2] != "" {
			if length, err = strconv.Atoi(m[2]); err != nil {
				continue
			}
		}

		if length == 0 {
			continue
		}

		out[current] = append(out[current], tool.LineRange{Start: start, End: start + length - 1})
	}

	return out
}
