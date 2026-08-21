package edit

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/kyleking/wavez/internal/tool"
)

// ErrSpanOutOfRange reports a span naming a line or column the file does not
// have, which means the file changed under whoever computed it.
var ErrSpanOutOfRange = errors.New("span is outside the file")

// Span is one replacement addressed by position rather than by text, as a
// language server states it: zero-based lines, and columns counted in UTF-16
// code units. It exists because a rename comes back from the server as
// positions, and re-deriving anchor text from them only to search for it
// again would reintroduce the ambiguity the server just resolved.
type Span struct {
	NewText   string
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

// ApplySpansToFile rewrites path with every span applied, writing atomically
// the way ApplyToFile does. Spans are applied last-first so an earlier one
// never shifts a later one's position, and overlapping spans are refused
// rather than silently resolved.
func ApplySpansToFile(path string, spans []Span) (tool.Change, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return tool.Change{}, fmt.Errorf("stat %s: %w", path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return tool.Change{}, fmt.Errorf("%s: %w", path, ErrSymlink)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path is the file this tool exists to edit
	if err != nil {
		return tool.Change{}, fmt.Errorf("reading %s: %w", path, err)
	}

	out, touched, err := ApplySpans(string(data), spans)
	if err != nil {
		return tool.Change{}, err
	}

	if err := writeAtomic(path, info.Mode(), []byte(out)); err != nil {
		return tool.Change{}, fmt.Errorf("writing %s: %w", path, err)
	}

	return tool.Change{Path: path, Added: touched, Removed: touched, Ranges: spanRanges(spans)}, nil
}

// ApplySpans returns src with every span applied, and how many lines the
// spans touched.
//
//nolint:gocritic // nonamedreturns forbids naming these; the sentence above carries their meaning
func ApplySpans(src string, spans []Span) (string, int, error) {
	lines := strings.Split(src, "\n")

	offsets := make([]int, len(spans))
	ends := make([]int, len(spans))

	for i, s := range spans {
		start, err := offsetOf(lines, s.Line, s.Column)
		if err != nil {
			return "", 0, err
		}

		end, err := offsetOf(lines, s.EndLine, s.EndColumn)
		if err != nil {
			return "", 0, err
		}

		if end < start {
			return "", 0, fmt.Errorf("%w: end before start at line %d", ErrSpanOutOfRange, s.Line+1)
		}

		offsets[i], ends[i] = start, end
	}

	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}

	sort.Slice(order, func(a, b int) bool { return offsets[order[a]] > offsets[order[b]] })

	out := src

	prevStart := len(src) + 1
	for _, i := range order {
		if ends[i] > prevStart {
			return "", 0, fmt.Errorf("%w: two spans overlap at line %d", ErrSpanOutOfRange, spans[i].Line+1)
		}

		out = out[:offsets[i]] + spans[i].NewText + out[ends[i]:]
		prevStart = offsets[i]
	}

	return out, touchedLines(spans), nil
}

// offsetOf converts a zero-based line and UTF-16 column into a byte offset
// into the joined source. The column is UTF-16 because that is what the
// protocol counts in, and a file with any character outside the basic plane
// would land in the wrong place if it were read as bytes or as runes.
func offsetOf(lines []string, line, column int) (int, error) {
	if line < 0 || line >= len(lines) {
		return 0, fmt.Errorf("%w: line %d of %d", ErrSpanOutOfRange, line+1, len(lines))
	}

	offset := 0
	for i := range line {
		offset += len(lines[i]) + 1
	}

	within, err := byteInLine(lines[line], column)
	if err != nil {
		return 0, err
	}

	return offset + within, nil
}

func byteInLine(line string, column int) (int, error) {
	if column < 0 {
		return 0, fmt.Errorf("%w: negative column", ErrSpanOutOfRange)
	}

	units := 0

	for i, r := range line {
		if units >= column {
			return i, nil
		}

		units += len(utf16.Encode([]rune{r}))
	}

	if units < column {
		return 0, fmt.Errorf("%w: column %d past the end of a %d-unit line", ErrSpanOutOfRange, column, units)
	}

	return len(line), nil
}

func touchedLines(spans []Span) int {
	seen := make(map[int]struct{}, len(spans))
	for _, s := range spans {
		for l := s.Line; l <= s.EndLine; l++ {
			seen[l] = struct{}{}
		}
	}

	return len(seen)
}

func spanRanges(spans []Span) []tool.LineRange {
	out := make([]tool.LineRange, 0, len(spans))
	for _, s := range spans {
		out = append(out, tool.LineRange{Start: s.Line + 1, End: s.EndLine + 1})
	}

	sort.Slice(out, func(a, b int) bool { return out[a].Start < out[b].Start })

	return out
}
