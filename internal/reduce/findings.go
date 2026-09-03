package reduce

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	// Below findingsMin, a reader is better served by the findings themselves
	// than by a shape summary of them.
	findingsMin = 12
	// A group names findingsSites of its sites, then says how many more.
	findingsSites = 3
	// These two bound the summary so a caller can skip windowing it. Both are
	// generous against real output: 349 ruff findings came to 22 kinds and 6
	// lines that were not findings.
	findingsKinds = 24
	findingsOther = 6
)

var (
	// Matches the `path:line[:col]: ` prefix a compiler, linter, or type
	// checker puts in front of a diagnostic. Non-greedy so a path holding a
	// colon takes the last one that still leaves digits behind it.
	findingSite = regexp.MustCompile(`^(\S+?:\d+(?::\d+)?): (\S.*)$`)
	// Masks the part of a message that names the thing rather than the rule, so
	// twenty missing docstrings read as one kind.
	findingLiteral = regexp.MustCompile("`[^`]*`|'[^']*'|\"[^\"]*\"")
	// Masks a standalone number. A rule code (`D102`) has no word boundary
	// before its digits and survives, which is what keeps D102 and D103 apart.
	findingCount = regexp.MustCompile(`\b\d+\b`)
)

// findings groups location-prefixed diagnostics by what they say, reporting
// each kind once with a count and a few sites. It trades the remaining sites
// for the shape, which a caller recovers by narrowing the check or reading the
// spilled output, and it claims nothing it cannot halve.
var findings = reducer{
	name:     "finding kinds",
	detect:   findingsGroupingPays,
	keep:     keepFindingKinds,
	complete: true,
}

type findingGroup struct {
	message string
	sites   []string
	n       int
}

type findingSet struct {
	files  map[string]bool
	groups []*findingGroup
	other  []string
	total  int
}

func groupFindings(lines []string) findingSet {
	set := findingSet{files: map[string]bool{}}
	byKind := map[string]*findingGroup{}

	for _, line := range lines {
		m := findingSite.FindStringSubmatch(strings.TrimLeft(line, " \t"))
		if m == nil {
			if t := strings.TrimSpace(line); t != "" {
				set.other = append(set.other, t)
			}

			continue
		}

		site, message := m[1], m[2]
		kind := findingCount.ReplaceAllString(findingLiteral.ReplaceAllString(message, "…"), "N")

		group := byKind[kind]
		if group == nil {
			group = &findingGroup{message: message}
			byKind[kind] = group
			set.groups = append(set.groups, group)
		}

		group.n++
		set.total++
		set.files[site[:strings.IndexByte(site, ':')]] = true

		if len(group.sites) < findingsSites {
			group.sites = append(group.sites, site)
		}
	}

	sort.SliceStable(set.groups, func(i, j int) bool { return set.groups[i].n > set.groups[j].n })

	return set
}

func findingsGroupingPays(lines []string) bool {
	set := groupFindings(lines)

	return set.total >= findingsMin && len(set.groups)*2 <= set.total
}

func keepFindingKinds(lines []string) []string {
	set := groupFindings(lines)

	kinds := set.groups
	unlisted := 0

	if len(kinds) > findingsKinds {
		for _, group := range kinds[findingsKinds:] {
			unlisted += group.n
		}

		kinds = kinds[:findingsKinds]
	}

	out := make([]string, 0, 2*len(kinds)+findingsOther+2)
	out = append(out, fmt.Sprintf(
		"%d findings across %d files in %d kinds, each listed once with up to %d of its sites:",
		set.total, len(set.files), len(set.groups), findingsSites,
	))

	for _, group := range kinds {
		sites := strings.Join(group.sites, ", ")
		if more := group.n - len(group.sites); more > 0 {
			sites = fmt.Sprintf("%s and %d more", sites, more)
		}

		out = append(out, fmt.Sprintf("%4dx %s", group.n, group.message), "     "+sites)
	}

	if unlisted > 0 {
		out = append(out, fmt.Sprintf(
			"%d further kinds holding %d findings are not listed. Narrow the check to see them",
			len(set.groups)-len(kinds), unlisted,
		))
	}

	if other := set.other; len(other) > findingsOther {
		out = append(out, other[len(other)-findingsOther:]...)
	} else {
		out = append(out, other...)
	}

	return out
}
