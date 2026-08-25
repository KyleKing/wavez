// Package edit implements str_replace: exact-match string replacement with a
// fuzzy fallback for whitespace-hallucinated anchors, as decided in
// DESIGN.md "Edits (v0.1)". It is pure and filesystem-free; ApplyToFile in
// apply.go is the thin wrapper that touches disk.
package edit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

var (
	// ErrNotFound is returned when old_string has no exact or fuzzy match.
	// Match against it with errors.Is, or errors.As into *NotFoundError for
	// the near-match candidate.
	ErrNotFound = errors.New("old_string not found in source")
	// ErrNotUnique is returned when old_string matches more than once. Match
	// against it with errors.Is, or errors.As into *NotUniqueError for every
	// matching line.
	ErrNotUnique = errors.New("old_string matches more than once")
	// ErrEmptyOldString is returned when old_string is empty.
	ErrEmptyOldString = errors.New("old_string must not be empty")
	// ErrNoChange is returned when old_string and new_string are identical.
	ErrNoChange = errors.New("old_string and new_string are identical")
)

// maxReportedLine bounds each line the near-match report echoes, so one
// minified or generated line cannot swamp the message.
const maxReportedLine = 200

// NotFoundError reports that old_string matched nothing in source. When a
// near match exists it names the one line the anchor got wrong, since that
// is what a model has to change: echoing the lines that already matched
// leaves it guessing, and a run measured on the fixed task set guessed twice
// and died.
type NotFoundError struct {
	// Sent and Found are the first line of old_string that differs from the
	// closest match, and the source line facing it.
	Sent  string
	Found string
	// CandidateLine is where the closest match starts in source, 1-indexed.
	CandidateLine int
	// MismatchLine is where Sent and Found part, 1-indexed in source.
	MismatchLine int
}

// Error implements error.
func (e *NotFoundError) Error() string {
	if e.CandidateLine == 0 {
		return ErrNotFound.Error()
	}

	return fmt.Sprintf("%s; the closest match starts at line %d and first differs at line %d:\n"+
		"  you sent:   %s\n  source has: %s",
		ErrNotFound, e.CandidateLine, e.MismatchLine, shown(e.Sent), shown(e.Found))
}

// shown renders a line for the report, naming a blank one rather than
// printing nothing after the label.
func shown(line string) string {
	if strings.TrimSpace(line) == "" {
		return "a blank line"
	}

	return clip(line)
}

func clip(line string) string {
	line = strings.TrimRight(line, " \t")
	if len(line) <= maxReportedLine {
		return line
	}

	return line[:maxReportedLine] + "…"
}

// Is reports that a *NotFoundError matches ErrNotFound, for errors.Is.
func (*NotFoundError) Is(target error) bool { return target == ErrNotFound }

// NotUniqueError reports that old_string matched more than once. Lines holds
// the 1-indexed start line of every match, in source order, so a model can
// widen its anchor to disambiguate.
type NotUniqueError struct {
	Lines []int
}

// Error implements error.
func (e *NotUniqueError) Error() string {
	return fmt.Sprintf("%s: %d matches at lines %v; widen old_string to make it unique",
		ErrNotUnique, len(e.Lines), e.Lines)
}

// Is reports that a *NotUniqueError matches ErrNotUnique, for errors.Is.
func (*NotUniqueError) Is(target error) bool { return target == ErrNotUnique }

// Result is the outcome of a successful Replace: the new source, the
// 1-indexed line range that changed in it, and line counts for building a
// tool.Change.
type Result struct {
	Source string
	// Note is what a caller has to be told about how the match was reached,
	// empty when the text matched as it was sent.
	Note    string
	Ranges  []tool.LineRange
	Added   int
	Removed int
	// Matches is how many occurrences were replaced, one unless the caller
	// asked for every occurrence.
	Matches int
}

// Replace substitutes oldString with newString in source. It tries an exact
// substring match first; if that matches zero times, it falls back to a
// fuzzy match that ignores each line's leading whitespace, splicing
// newString back in with source's actual indentation rather than
// oldString's. Either strategy must yield exactly one match, or Replace
// returns *NotFoundError or *NotUniqueError. The source's line ending style
// (LF or CRLF) is preserved exactly.
func Replace(source, oldString, newString string) (Result, error) {
	return replaceWithRepair(source, oldString, newString, false)
}

// Holds reports whether source contains anchor, resolved by the same ladder
// Replace matches with, so "the file already reads that way" and "an edit
// here would match" can never disagree.
func Holds(source, anchor string) bool {
	if anchor == "" {
		return false
	}

	_, err := doReplace(source, anchor, anchor+"\x00", false)

	return err == nil || errors.Is(err, ErrNotUnique)
}

// ReplaceAll substitutes every occurrence of oldString, where Replace
// refuses more than one. Two occurrences of the same text are not always a
// mistake: a rename touches a call written identically at four sites, and
// no widening makes them distinguishable, so "widen old_string" asks for
// something the file cannot give. One replay lane died stagnant sending the
// same pair five times against that answer.
//
// It is a separate entry point rather than a flag with a default, so the
// broader edit is only ever the one the caller named.
func ReplaceAll(source, oldString, newString string) (Result, error) {
	return replaceWithRepair(source, oldString, newString, true)
}

func replaceWithRepair(source, oldString, newString string, all bool) (Result, error) {
	res, err := attempt(source, oldString, newString, all)
	if !errors.Is(err, ErrNotFound) {
		return res, err
	}

	// The anchor may never have left the model in the shape it was written:
	// see restoreEntities. The replacement is repaired with it, since the
	// same collapse hit both halves and writing the collapsed rune into the
	// file would leave it unparsable.
	repairedOld, changed := restoreEntities(oldString)
	if !changed {
		return res, err
	}

	repairedNew, _ := restoreEntities(newString)

	repaired, repairErr := attempt(source, repairedOld, repairedNew, all)
	if repairErr != nil {
		return res, err
	}

	repaired.Note = "old_string and new_string arrived with an HTML character reference in " +
		"place of an ampersand and a name, and were repaired to match the file. Text you " +
		"send may be mangled that way in transit."

	return repaired, nil
}

func attempt(source, oldString, newString string, all bool) (Result, error) {
	if oldString == "" {
		return Result{}, ErrEmptyOldString
	}

	if oldString == newString {
		return Result{}, ErrNoChange
	}

	crlf := strings.Contains(source, "\r\n")

	normSource, normOld, normNew := source, oldString, newString
	if crlf {
		normSource = strings.ReplaceAll(source, "\r\n", "\n")
		normOld = strings.ReplaceAll(oldString, "\r\n", "\n")
		normNew = strings.ReplaceAll(newString, "\r\n", "\n")
	}

	spliced, err := doReplace(normSource, normOld, normNew, all)
	if err != nil {
		return Result{}, err
	}

	newSource := spliced.source
	if crlf {
		newSource = strings.ReplaceAll(newSource, "\n", "\r\n")
	}

	ranges := spliced.ranges
	if len(ranges) == 0 {
		ranges = []tool.LineRange{lineRange(spliced.startLine, spliced.added)}
	}

	return Result{
		Source:  newSource,
		Ranges:  ranges,
		Added:   spliced.added,
		Removed: spliced.removed,
		Matches: max(spliced.matches, 1),
	}, nil
}

func lineRange(startLine, added int) tool.LineRange {
	if added == 0 {
		return tool.LineRange{Start: startLine, End: startLine - 1}
	}

	return tool.LineRange{Start: startLine, End: startLine + added - 1}
}

// splice is the outcome of locating and substituting one match: the new
// source text plus the accounting Replace needs to build a Result.
type splice struct {
	source string
	// ranges is set only when more than one occurrence moved, since a
	// single one is derived from startLine and added.
	ranges    []tool.LineRange
	startLine int
	added     int
	removed   int
	matches   int
}

func doReplace(source, oldString, newString string, all bool) (splice, error) {
	idxs := findAll(source, oldString)

	switch {
	case len(idxs) == 0:
		return spliceFuzzy(source, oldString, newString, all)
	case len(idxs) == 1:
		return spliceExact(source, oldString, newString, idxs[0]), nil
	case all:
		return spliceEvery(source, oldString, newString, idxs), nil
	default:
		return splice{}, &NotUniqueError{Lines: matchLines(source, idxs)}
	}
}

// spliceEvery replaces each exact occurrence, reporting one range per
// occurrence in the text it produces rather than in the text it read.
func spliceEvery(source, oldString, newString string, idxs []int) splice {
	var b strings.Builder

	ranges := make([]tool.LineRange, 0, len(idxs))
	added, removed, delta, prev := 0, 0, 0, 0

	for _, idx := range idxs {
		b.WriteString(source[prev:idx])

		startLine := 1 + strings.Count(source[:idx], "\n") + delta
		ranges = append(ranges, lineRange(startLine, countLines(newString)))

		b.WriteString(newString)

		added += countLines(newString)
		removed += countLines(oldString)
		delta += strings.Count(newString, "\n") - strings.Count(oldString, "\n")
		prev = idx + len(oldString)
	}

	b.WriteString(source[prev:])

	return splice{
		source:    b.String(),
		ranges:    ranges,
		startLine: ranges[0].Start,
		added:     added,
		removed:   removed,
		matches:   len(idxs),
	}
}

func findAll(source, oldString string) []int {
	var idxs []int

	offset := 0
	for {
		i := strings.Index(source[offset:], oldString)
		if i < 0 {
			break
		}

		idxs = append(idxs, offset+i)
		offset += i + len(oldString)
	}

	return idxs
}

func matchLines(source string, idxs []int) []int {
	lines := make([]int, len(idxs))
	for i, idx := range idxs {
		lines[i] = 1 + strings.Count(source[:idx], "\n")
	}

	return lines
}

func countLines(s string) int {
	if s == "" {
		return 0
	}

	return strings.Count(s, "\n") + 1
}

func spliceExact(source, oldString, newString string, idx int) splice {
	startLine := 1 + strings.Count(source[:idx], "\n")
	newSource := source[:idx] + newString + source[idx+len(oldString):]

	return splice{
		source:    newSource,
		startLine: startLine,
		added:     countLines(newString),
		removed:   countLines(oldString),
	}
}

func spliceFuzzy(source, oldString, newString string, all bool) (splice, error) {
	sourceLines := strings.Split(source, "\n")
	oldLines := strings.Split(oldString, "\n")

	starts := fuzzyMatches(sourceLines, oldLines)

	switch {
	case len(starts) == 0:
		return spliceOverBlanks(source, oldString, newString, sourceLines, oldLines, all)
	case len(starts) == 1:
	case all:
		spans := make([][2]int, len(starts))
		for i, s := range starts {
			spans[i] = [2]int{s, s + len(oldLines) - 1}
		}

		return spliceEverySpan(sourceLines, oldLines, newString, spans), nil
	default:
		lines := make([]int, len(starts))
		for i, s := range starts {
			lines[i] = s + 1
		}

		return splice{}, &NotUniqueError{Lines: lines}
	}

	start := starts[0]
	n := len(oldLines)

	realIndents := make([]string, n)
	for j := range n {
		realIndents[j] = leadingWhitespace(sourceLines[start+j])
	}

	var newLines []string
	if newString != "" {
		newLines = strings.Split(newString, "\n")
	}

	reindented := make([]string, len(newLines))
	for j, l := range newLines {
		indent := realIndents[n-1]
		if j < n {
			indent = realIndents[j]
		}

		reindented[j] = indent + strings.TrimLeft(l, " \t")
	}

	result := make([]string, 0, len(sourceLines)-n+len(reindented))
	result = append(result, sourceLines[:start]...)
	result = append(result, reindented...)
	result = append(result, sourceLines[start+n:]...)

	return splice{
		source:    strings.Join(result, "\n"),
		startLine: start + 1,
		added:     len(newLines),
		removed:   n,
	}, nil
}

// spliceOverBlanks matches an anchor whose blank lines the caller dropped.
// Half of every near-match report this project logged faced a blank source
// line, which is what an anchor copied without the file's blank lines looks
// like from the inside, and the report for it said only "source has: " with
// nothing after it.
//
// It matches the anchor's non-blank lines in order, letting the source hold
// blank lines the anchor does not, and replaces the whole source span it
// covered. Leading whitespace is ignored on both sides, as the line-wise
// match already does.
func spliceOverBlanks(
	source, oldString, newString string, sourceLines, oldLines []string, all bool,
) (splice, error) {
	wanted := nonBlank(oldLines)
	if len(wanted) == 0 {
		return splice{}, notFoundError(source, oldString, sourceLines, oldLines)
	}

	var spans [][2]int

	for i := range sourceLines {
		if end, ok := spanOverBlanks(sourceLines, wanted, i); ok {
			spans = append(spans, [2]int{i, end})
		}
	}

	switch {
	case len(spans) == 0:
		return splice{}, notFoundError(source, oldString, sourceLines, oldLines)
	case len(spans) == 1:
	case all:
		return spliceEverySpan(sourceLines, oldLines, newString, spans), nil
	default:
		lines := make([]int, len(spans))
		for i, s := range spans {
			lines[i] = s[0] + 1
		}

		return splice{}, &NotUniqueError{Lines: lines}
	}

	return spliceSpan(sourceLines, oldLines, newString, spans[0][0], spans[0][1]), nil
}

// spanOverBlanks reports where the anchor's lines finish when matched from
// start, skipping blank source lines between them. A blank line inside the
// anchor matches nothing of its own, since the source's blanks are what the
// span already absorbs.
func spanOverBlanks(sourceLines, wanted []string, start int) (int, bool) {
	// The span begins on the anchor's first line rather than on a blank one
	// ahead of it, or every blank line before a match would start one too.
	if collapseSpacing(sourceLines[start]) != wanted[0] {
		return 0, false
	}

	i, end := start, start

	for _, want := range wanted {
		for i < len(sourceLines) && blankLine(sourceLines[i]) {
			i++
		}

		if i >= len(sourceLines) || collapseSpacing(sourceLines[i]) != want {
			return 0, false
		}

		end, i = i, i+1
	}

	return end, true
}

func nonBlank(lines []string) []string {
	out := make([]string, 0, len(lines))

	for _, l := range lines {
		if t := collapseSpacing(l); t != "" {
			out = append(out, t)
		}
	}

	return out
}

// spliceSpan replaces sourceLines[start:end+1] with newString, shifting it
// by the difference between the anchor's own indentation and the source's.
// Reindenting line by line is what the line-wise match does, and it cannot
// work here: the source span holds blank lines the anchor does not, so
// position no longer pairs a replacement line with the line it faces.
func spliceSpan(sourceLines, oldLines []string, newString string, start, end int) splice {
	var newLines []string
	if newString != "" {
		newLines = strings.Split(newString, "\n")
	}

	from, to := firstIndent(oldLines), leadingWhitespace(sourceLines[start])

	shifted := make([]string, len(newLines))
	for j, l := range newLines {
		shifted[j] = to + strings.TrimPrefix(l, from)
		if blankLine(l) {
			shifted[j] = l
		}
	}

	span := end - start + 1

	result := make([]string, 0, len(sourceLines)-span+len(shifted))
	result = append(result, sourceLines[:start]...)
	result = append(result, shifted...)
	result = append(result, sourceLines[end+1:]...)

	return splice{
		source:    strings.Join(result, "\n"),
		startLine: start + 1,
		added:     len(newLines),
		removed:   span,
	}
}

// spliceEverySpan replaces every matched span, working from the last one
// back so an earlier span's line numbers are still the ones it was found
// at. Each occurrence is reindented against the source lines it faces, as
// a single fuzzy match is.
func spliceEverySpan(sourceLines, oldLines []string, newString string, spans [][2]int) splice {
	lines := sourceLines
	added, removed := 0, 0

	for i := len(spans) - 1; i >= 0; i-- {
		one := spliceSpan(lines, oldLines, newString, spans[i][0], spans[i][1])
		lines = strings.Split(one.source, "\n")
		added += one.added
		removed += one.removed
	}

	ranges := make([]tool.LineRange, 0, len(spans))
	delta := 0

	for _, span := range spans {
		startLine := span[0] + 1 + delta
		ranges = append(ranges, lineRange(startLine, countLines(newString)))
		delta += countLines(newString) - (span[1] - span[0] + 1)
	}

	return splice{
		source:    strings.Join(lines, "\n"),
		ranges:    ranges,
		startLine: ranges[0].Start,
		added:     added,
		removed:   removed,
		matches:   len(spans),
	}
}

// firstIndent is the indentation of the first line that has any content,
// which is what the anchor was written against.
func firstIndent(lines []string) string {
	for _, l := range lines {
		if !blankLine(l) {
			return leadingWhitespace(l)
		}
	}

	return ""
}

func fuzzyMatches(sourceLines, oldLines []string) []int {
	var starts []int

	n := len(oldLines)
	for i := 0; i+n <= len(sourceLines); i++ {
		if blockMatches(sourceLines[i:i+n], oldLines) {
			starts = append(starts, i)
		}
	}

	return starts
}

func blockMatches(block, oldLines []string) bool {
	for j := range oldLines {
		if !sameLine(block[j], oldLines[j]) {
			return false
		}
	}

	return true
}

// closestMatch is where in source the anchor most nearly sits, -1 when no
// line of it appears at all.
//
// An alignment whose first line matches wins over a higher-scoring one that
// slides past it. Scoring alone picks whichever offset agrees on the most
// lines, and a source that gained one line the anchor does not have scores
// higher shifted by one, which makes the report say the anchor's first line
// is wrong when that line is exactly where it belongs. One replay lane
// resent the same anchor five times reading that.
func closestMatch(sourceLines, oldLines []string) int {
	n := len(oldLines)
	anchored, anchoredScore := -1, 0
	best, bestScore := -1, 0

	for i := 0; i+n <= len(sourceLines); i++ {
		score := 0
		for j := range oldLines {
			if sameLine(sourceLines[i+j], oldLines[j]) {
				score++
			}
		}

		if score > bestScore {
			best, bestScore = i, score
		}

		if score > anchoredScore && sameLine(sourceLines[i], oldLines[0]) {
			anchored, anchoredScore = i, score
		}
	}

	if anchored >= 0 {
		return anchored
	}

	return best
}

// minPrefixMatch is how much of an anchor has to be right before saying
// where it goes wrong helps. Below it the report would point at a
// coincidence.
const minPrefixMatch = 8

// prefixMismatch is the report for an anchor no line of which matches: it
// finds how far into the anchor the source agrees and shows what parts
// there. A model that closed a JSON string with a typographic quote sent an
// anchor whose only fault was three trailing characters, and got back
// nothing but "not found", so it sent the same anchor twice more and the run
// died stagnant.
func prefixMismatch(source, oldString string) error {
	n := longestPrefix(source, oldString)
	if n < minPrefixMatch {
		return &NotFoundError{}
	}

	idx := strings.Index(source, oldString[:n])

	return &NotFoundError{
		CandidateLine: strings.Count(source[:idx], "\n") + 1,
		MismatchLine:  strings.Count(source[:idx+n], "\n") + 1,
		Sent:          restOfLine(oldString[n:]),
		Found:         restOfLine(source[idx+n:]),
	}
}

// half is the binary search's divisor.
const half = 2

// longestPrefix is the length of the longest prefix of want that occurs in
// source. A prefix of a prefix that occurs also occurs, so the length is
// monotonic and a binary search over it is exact.
func longestPrefix(source, want string) int {
	lo, hi := 0, len(want)
	for lo < hi {
		mid := lo + (hi-lo+1)/half
		if strings.Contains(source, want[:mid]) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	return lo
}

func restOfLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}

	if s == "" {
		return "(end of line)"
	}

	return s
}

// sameLine reports whether two lines are the same code written with
// different spacing. It is the one comparison every fuzzy rung makes, and
// its tolerance is deliberately the formatter's freedom: the format gate
// runs gofmt over each file the moment an edit lands, and gofmt's own
// column alignment is interior whitespace. An anchor copied from what the
// model wrote reads `name: "x",` where the file now holds `name:  "x",`,
// so comparing on leading whitespace alone loses to a rewrite the harness
// performed itself.
func sameLine(a, b string) bool {
	return collapseSpacing(a) == collapseSpacing(b)
}

// collapseSpacing trims a line and reduces every run of spaces and tabs
// inside it to one space.
func collapseSpacing(s string) string {
	var (
		b     strings.Builder
		space bool
	)

	for _, r := range strings.TrimSpace(s) {
		if r == ' ' || r == '\t' {
			space = true

			continue
		}

		if space {
			b.WriteByte(' ')

			space = false
		}

		b.WriteRune(r)
	}

	return b.String()
}

// blankLine reports a line with nothing but whitespace on it.
func blankLine(s string) bool { return collapseSpacing(s) == "" }

func leadingWhitespace(s string) string {
	trimmed := strings.TrimLeft(s, " \t")
	return s[:len(s)-len(trimmed)]
}

func notFoundError(source, oldString string, sourceLines, oldLines []string) error {
	bestIdx := closestMatch(sourceLines, oldLines)
	if bestIdx < 0 {
		return prefixMismatch(source, oldString)
	}

	for j := range oldLines {
		if sameLine(sourceLines[bestIdx+j], oldLines[j]) {
			continue
		}

		return &NotFoundError{
			CandidateLine: bestIdx + 1,
			MismatchLine:  bestIdx + j + 1,
			Sent:          oldLines[j],
			Found:         sourceLines[bestIdx+j],
		}
	}

	return &NotFoundError{}
}
