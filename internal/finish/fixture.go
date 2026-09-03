package finish

import (
	"path"
	"strings"

	"github.com/kyleking/wavez/internal/glob"
)

// FixturesAreAccountedFor reports every golden fixture the run rewrote that
// its closing answer never mentions.
//
// A golden file is compared byte for byte, so a tool regenerates it rather
// than a person editing it, and regenerating one is how a run makes its own
// change pass the check that was there to catch it. A lane broke a Textual
// layout, ran the snapshot updater, and shipped green with every test
// passing. Nothing here judges whether the regeneration was right: naming it
// is what puts the diff in front of a reader, and a run that reviewed the
// frames has already written the sentence this asks for.
func FixturesAreAccountedFor(answer string, changed, patterns []string) Report {
	if len(patterns) == 0 {
		return Report{}
	}

	var report Report

	for _, rel := range changed {
		if !matchesAny(patterns, rel) || namesFile(answer, rel) {
			continue
		}

		report.Findings = append(report.Findings, Finding{
			Check: "the run rewrote a golden fixture its answer never mentions", Detail: rel,
		})
	}

	return report
}

func matchesAny(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if glob.Match(pattern, rel) {
			return true
		}
	}

	return false
}

// namesFile reports whether the answer refers to rel, by its path or by the
// base name a sentence would use.
func namesFile(answer, rel string) bool {
	return strings.Contains(answer, rel) || strings.Contains(answer, path.Base(rel))
}
