package route

import (
	"regexp"
	"strings"

	"github.com/kyleking/wavez/internal/decide"
)

// The bounds below were set on bench/route_corpus.jsonl: a rule that is
// wrong on a line there is narrowed until it is not.

// maxDirectWords is the longest prompt the direct rule answers.
const maxDirectWords = 90

// maxRenameWords is the longest prompt taken to be one rename.
const maxRenameWords = 40

// minPieces is how many list items make an enumeration.
const minPieces = 3

// listItem matches one line of a bulleted or numbered list.
var listItem = regexp.MustCompile(`(?m)^[ \t]*(?:[-*•]|\d+[.)])[ \t]+\S`)

// inlineItem matches "(1)" or "1)" written inside a sentence.
var inlineItem = regexp.MustCompile(`(?:^|\s)\(?\d\)\s`)

// independent is wording that says the pieces of a list stand alone.
var independent = regexp.MustCompile(`(?i)\b(?:independent(?:ly)?|unrelated|separate(?:ly)?|in parallel)\b`)

// sequential is wording that says the pieces of a list are steps.
var sequential = regexp.MustCompile(`(?i)\b(?:then|after that|afterwards|once that|finally|step \d|depends on)\b`)

// searching is wording that says the target still has to be found or
// understood, whatever paths the prompt also names.
var searching = regexp.MustCompile(`(?i)\b(?:why|how|investigate|figure out|find|search|look for|look into|` +
	`diagnose|somewhere|sometimes|flaky|intermittent(?:ly)?|audit|review|explain|understand|` +
	`plan|design|propose|suggest|should we|whether|compare|summari[sz]e)\b`)

// sweeping is wording that widens a change past the paths named.
var sweeping = regexp.MustCompile(`(?i)\b(?:every|all|each|any|across|anywhere|everywhere|wherever|throughout|` +
	`whole|entire|rest of)\b`)

// asking opens a prompt that is a question, with or without its mark.
var asking = regexp.MustCompile(`(?i)^(?:where|which|what|who|when|is|are|does|do|did|can|could|should|would)\b`)

// renaming opens a prompt that asks for one rename.
var renaming = regexp.MustCompile(`(?i)^rename\b`)

// locating opens a prompt that asks where something is.
var locating = regexp.MustCompile(`(?i)^(?:where\b|which (?:file|package|function|method|module|test|type)\b|` +
	`find (?:where|which|out where)\b|locate\b|track down\b)`)

// Rules returns the rules that answer the route question without a model,
// in the order they are tried. Each takes a state as State builds it, and
// each passes on anything it is not sure of.
func Rules() []decide.Rule {
	return []decide.Rule{coordinateRule, directRule, exploreRule}
}

// pieces counts the enumerated items of a prompt.
func pieces(prompt string) int {
	return max(len(listItem.FindAllString(prompt, -1)), len(inlineItem.FindAllString(prompt, -1)))
}

// coordinateRule fires on a list of three or more items that the prompt
// itself calls independent, and never on a list of steps.
func coordinateRule(state string) (string, bool) {
	p := parse(state)

	if pieces(p.prompt) < minPieces || !independent.MatchString(p.prompt) || sequential.MatchString(p.prompt) {
		return "", false
	}

	return Coordinate, true
}

// directRule fires on a short prompt with no list and no question in it
// that names a file that exists, or that asks for one rename under a path
// that exists. A rename may say "every caller", because the callers are the
// rename tool's to find.
func directRule(state string) (string, bool) {
	p := parse(state)
	words := len(strings.Fields(p.prompt))

	if pieces(p.prompt) > 0 || strings.Contains(p.prompt, "?") ||
		asking.MatchString(p.prompt) || searching.MatchString(p.prompt) {
		return "", false
	}

	if renaming.MatchString(p.prompt) && words <= maxRenameWords && len(p.files)+len(p.dirs) > 0 {
		return Direct, true
	}

	if len(p.files) == 0 || words > maxDirectWords || sweeping.MatchString(p.prompt) {
		return "", false
	}

	return Direct, true
}

// exploreRule fires on a prompt that opens by asking where something is
// and names no path that exists.
func exploreRule(state string) (string, bool) {
	p := parse(state)

	if len(p.files)+len(p.dirs) > 0 || pieces(p.prompt) > 0 || !locating.MatchString(p.prompt) {
		return "", false
	}

	return Explore, true
}
