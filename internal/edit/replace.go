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

// maxCandidateLines bounds the near-match echoed back to the model. The
// candidate is as long as old_string was, so an oversized anchor would
// otherwise return most of the file and double the context it already cost.
const maxCandidateLines = 12

// NotFoundError reports that old_string matched nothing in source. Candidate
// fields are populated when a whitespace-normalized near match exists, so a
// model can compare its anchor against the source's actual indentation.
type NotFoundError struct {
	CandidateText string
	CandidateLine int
	// CandidateElided counts lines trimmed from the end of CandidateText.
	CandidateElided int
}

// Error implements error.
func (e *NotFoundError) Error() string {
	if e.CandidateText == "" {
		return ErrNotFound.Error()
	}

	if e.CandidateElided > 0 {
		return fmt.Sprintf("%s; closest near match at line %d (%d more lines not shown):\n%s",
			ErrNotFound, e.CandidateLine, e.CandidateElided, e.CandidateText)
	}

	return fmt.Sprintf("%s; closest near match at line %d:\n%s", ErrNotFound, e.CandidateLine, e.CandidateText)
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
		return splice{}, notFoundError(sourceLines, oldLines)
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

func trimLeading(s string) string {
	return strings.TrimLeft(s, " \t")
}

func leadingWhitespace(s string) string {
	trimmed := strings.TrimLeft(s, " \t")
	return s[:len(s)-len(trimmed)]
}

func notFoundError(sourceLines, oldLines []string) error {
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
		return &NotFoundError{}
	}

	shown, elided := n, 0
	if shown > maxCandidateLines {
		shown, elided = maxCandidateLines, n-maxCandidateLines
	}

	return &NotFoundError{
		CandidateLine:   bestIdx + 1,
		CandidateText:   strings.Join(sourceLines[bestIdx:bestIdx+shown], "\n"),
		CandidateElided: elided,
	}
}
