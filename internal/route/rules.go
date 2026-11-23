package route

import (
	"regexp"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/decide"
)

// A rule here answers only when two things hold together: the whole prompt
// has one closed shape, and the paths that shape depends on exist. Each
// shape is a list of what is accepted, never a list of words to avoid, so a
// prompt nobody thought of passes to the model or to silence. A hint that
// is wrong costs the agent more than no hint.

// maxLocateWords is the longest question taken to ask only where something
// is.
const maxLocateWords = 30

// minPieces is how many list items make work worth splitting.
const minPieces = 3

// ident is a Go-style name, optionally qualified and optionally quoted.
const ident = "`?([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)?)`?"

// kind is the optional words between "rename" and the name.
const kind = `(?:(?:the|a) )?(?:(?:unexported|exported|private|public) )?` +
	`(?:(?:function|func|method|type|struct|interface|constant|const|variable|var|field) )?`

// callers is the one clause a rename may carry after the new name. The
// callers are the rename tool's to find, so naming them adds no survey.
const callers = `(?: and update (?:every|all|its|the) (?:callers?|call sites?|references?|uses?)` +
	`(?: in (?:the |this )?(?:[\w-]+ )?(?:package|repository|repo|module|codebase|project|tree))?` +
	`(?:, (?:tests|comments|docs)(?:(?:,| and) (?:tests|comments|docs))* included)?)?`

// The two ways a prompt writes one rename: "Rename X in path to Y" and
// "Rename path:X to Y". Each matches the whole prompt or nothing.
var (
	renameIn = regexp.MustCompile(`(?i)^rename ` + kind + ident + ` in (\S+) to ` + ident + callers + `\.?$`)
	renameAt = regexp.MustCompile(`(?i)^rename ` + kind + "`?" + `([^\s:` + "`" + `]+):` + ident + ` to ` + ident +
		callers + `\.?$`)
)

// The ways a prompt opens when it asks only where something is. Each names
// what is being looked for with an article, which is what keeps out a
// question about an opinion ("where do you think", "which package would
// you") and one about the conversation ("where does that leave us").
var locating = regexp.MustCompile(`(?i)^(?:` +
	`where (?:is|are|does|do|did) (?:the|a|an|our) ` +
	`|which (?:file|package|function|method|module|test|type) ` +
	`(?:defines|declares|contains|holds|implements|handles|decides|owns|calls|sets|registers|parses|builds|` +
	`configures|controls|runs) ` +
	`|find (?:out )?where (?:the|a|an|our|we) ` +
	`|locate the (?:code|function|method|file|type|test|package|module|place|line|handler|definition|declaration) ` +
	`)`)

// secondSentence matches the start of anything after a first sentence.
var secondSentence = regexp.MustCompile(`[.?!;:]\s+\S|\n`)

// listItem matches a line that starts a bulleted or numbered item.
var listItem = regexp.MustCompile(`^[ \t]*(?:[-*•]|\d+[.)])[ \t]+\S`)

// independent is the author saying the pieces of a list stand alone.
var independent = regexp.MustCompile(`(?i)\b(?:independent(?:ly)?|unrelated|separate(?:ly)?|in parallel)\b`)

// sequential is wording that says the pieces of a list are steps.
var sequential = regexp.MustCompile(`(?i)\b(?:then|after that|afterwards|once that|finally|step \d|depends on)\b`)

// Rules returns the rules that answer the route question without a model,
// in the order they are tried. Each takes a state as State builds it, and
// each passes on anything it has not checked.
func Rules() []decide.Rule {
	return []decide.Rule{renameRule, coordinateRule, locateRule}
}

// renameRule answers Direct when the whole prompt is one rename of one
// name, and the file or directory it says the name is in exists.
func renameRule(state string) (string, bool) {
	p := parse(state)
	if p.cut {
		return "", false
	}

	var path, from, to string

	if m := renameIn.FindStringSubmatch(p.prompt); m != nil {
		from, path, to = m[1], m[2], m[3]
	} else if m := renameAt.FindStringSubmatch(p.prompt); m != nil {
		path, from, to = m[1], m[2], m[3]
	} else {
		return "", false
	}

	if from == to || !slices.Contains(p.paths, clean(path)) {
		return "", false
	}

	return Direct, true
}

// clean writes a path token the way a state lists it.
func clean(path string) string {
	path = strings.Trim(path, "\"'`")

	return strings.TrimPrefix(strings.TrimSuffix(path, "/"), "./")
}

// locateRule answers Explore when the whole prompt is one short sentence
// asking where something is, and it names no path that exists.
func locateRule(state string) (string, bool) {
	p := parse(state)

	if p.cut || len(p.paths) > 0 || !locating.MatchString(p.prompt) ||
		secondSentence.MatchString(p.prompt) || len(strings.Fields(p.prompt)) > maxLocateWords {
		return "", false
	}

	return Explore, true
}

// coordinateRule answers Coordinate when the prompt is a list of three or
// more items under a lead-in that calls them independent, and code can see
// that they are: every item names a path that exists, and no two items
// touch the same path or one inside the other.
func coordinateRule(state string) (string, bool) {
	p := parse(state)
	if p.cut || sequential.MatchString(p.prompt) {
		return "", false
	}

	lead, items := list(p.prompt)
	if len(items) < minPieces || !independent.MatchString(lead) {
		return "", false
	}

	var taken []string

	for _, item := range items {
		var own []string

		for _, token := range candidates(item) {
			if slices.Contains(p.paths, token) {
				own = append(own, token)
			}
		}

		if len(own) == 0 || slices.ContainsFunc(own, func(path string) bool { return overlaps(path, taken) }) {
			return "", false
		}

		taken = append(taken, own...)
	}

	return Coordinate, true
}

// list splits a prompt into the text above its first list item and the
// items, each with the lines that continue it.
func list(prompt string) (string, []string) {
	var (
		lead  []string
		items []string
	)

	for line := range strings.SplitSeq(prompt, "\n") {
		switch {
		case listItem.MatchString(line):
			items = append(items, line)
		case len(items) == 0:
			lead = append(lead, line)
		default:
			items[len(items)-1] += "\n" + line
		}
	}

	return strings.Join(lead, "\n"), items
}

// overlaps reports whether path is one of others, holds one, or is inside
// one.
func overlaps(path string, others []string) bool {
	return slices.ContainsFunc(others, func(other string) bool {
		return path == other || strings.HasPrefix(path, other+"/") || strings.HasPrefix(other, path+"/")
	})
}
