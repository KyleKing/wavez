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
	Source  string
	Ranges  []tool.LineRange
	Added   int
	Removed int
}

// Replace substitutes oldString with newString in source. It tries an exact
// substring match first; if that matches zero times, it falls back to a
// fuzzy match that ignores each line's leading whitespace, splicing
// newString back in with source's actual indentation rather than
// oldString's. Either strategy must yield exactly one match, or Replace
// returns *NotFoundError or *NotUniqueError. The source's line ending style
// (LF or CRLF) is preserved exactly.
func Replace(source, oldString, newString string) (Result, error) {
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

	spliced, err := doReplace(normSource, normOld, normNew)
	if err != nil {
		return Result{}, err
	}

	newSource := spliced.source
	if crlf {
		newSource = strings.ReplaceAll(newSource, "\n", "\r\n")
	}

	return Result{
		Source:  newSource,
		Ranges:  []tool.LineRange{lineRange(spliced.startLine, spliced.added)},
		Added:   spliced.added,
		Removed: spliced.removed,
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
	source    string
	startLine int
	added     int
	removed   int
}

func doReplace(source, oldString, newString string) (splice, error) {
	idxs := findAll(source, oldString)

	switch len(idxs) {
	case 0:
		return spliceFuzzy(source, oldString, newString)
	case 1:
		return spliceExact(source, oldString, newString, idxs[0]), nil
	default:
		return splice{}, &NotUniqueError{Lines: matchLines(source, idxs)}
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

func spliceFuzzy(source, oldString, newString string) (splice, error) {
	sourceLines := strings.Split(source, "\n")
	oldLines := strings.Split(oldString, "\n")

	starts := fuzzyMatches(sourceLines, oldLines)

	switch len(starts) {
	case 0:
		return spliceOverBlanks(source, oldString, newString, sourceLines, oldLines)
	case 1:
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
func spliceOverBlanks(source, oldString, newString string, sourceLines, oldLines []string) (splice, error) {
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

	switch len(spans) {
	case 0:
		return splice{}, notFoundError(source, oldString, sourceLines, oldLines)
	case 1:
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
	if trimLeading(sourceLines[start]) != wanted[0] {
		return 0, false
	}

	i, end := start, start

	for _, want := range wanted {
		for i < len(sourceLines) && trimLeading(sourceLines[i]) == "" {
			i++
		}

		if i >= len(sourceLines) || trimLeading(sourceLines[i]) != want {
			return 0, false
		}

		end, i = i, i+1
	}

	return end, true
}

func nonBlank(lines []string) []string {
	out := make([]string, 0, len(lines))

	for _, l := range lines {
		if t := trimLeading(l); t != "" {
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
		if trimLeading(l) == "" {
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

// firstIndent is the indentation of the first line that has any content,
// which is what the anchor was written against.
func firstIndent(lines []string) string {
	for _, l := range lines {
		if trimLeading(l) != "" {
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
		if trimLeading(block[j]) != trimLeading(oldLines[j]) {
			return false
		}
	}

	return true
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

func trimLeading(s string) string {
	return strings.TrimLeft(s, " \t")
}

func leadingWhitespace(s string) string {
	trimmed := strings.TrimLeft(s, " \t")
	return s[:len(s)-len(trimmed)]
}

func notFoundError(source, oldString string, sourceLines, oldLines []string) error {
	n := len(oldLines)

	bestScore, bestIdx := 0, -1
	for i := 0; i+n <= len(sourceLines); i++ {
		score := 0
		for j := range oldLines {
			if trimLeading(sourceLines[i+j]) == trimLeading(oldLines[j]) {
				score++
			}
		}

		if score > bestScore {
			bestScore, bestIdx = score, i
		}
	}

	if bestIdx < 0 {
		return prefixMismatch(source, oldString)
	}

	for j := range oldLines {
		if trimLeading(sourceLines[bestIdx+j]) == trimLeading(oldLines[j]) {
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
