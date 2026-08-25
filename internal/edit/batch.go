package edit

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// Pair is one replacement in a batch: the exact text to find and what
// replaces it.
type Pair struct {
	OldString string
	NewString string
	// All replaces every occurrence rather than refusing more than one.
	// Off by default, so the wider edit is only ever the one asked for.
	All bool
}

// ApplyAllToFile applies every pair to path, reading and writing the file
// once. It is all or nothing: a pair that matches zero or several places
// fails the whole batch with that pair's own error and leaves the file
// untouched, so a half-applied edit is never a state the caller has to
// reason about.
//
// Every anchor is resolved against the file as it was read, not against the
// text the previous pair produced, because that is the only version the
// caller has seen. Applying them in sequence made the second anchor of a
// batch search a file that no longer existed anywhere, and the failure was
// unreadable: measured over this project's thread logs, a batch of one
// fails 27% of the time and a batch of two 67%, and the extra failures are
// anchors an earlier pair had already consumed.
//
// Two pairs whose spans overlap are refused by index, since which one wins
// is undecidable and silently picking one is worse than saying so.
//
// The reported Ranges are one span covering every line that moved, computed
// from the file's before and after rather than from each replacement, since
// a later edit shifts the line numbers an earlier one reported.
func ApplyAllToFile(path string, pairs []Pair) (tool.Change, error) {
	batch, err := ApplyToFiles([]FileEdit{{Path: path, Pairs: pairs}})
	if err != nil {
		return tool.Change{}, err
	}

	return batch.Changes[0], nil
}

// FileEdit is one file's batch of replacements.
type FileEdit struct {
	Path  string
	Pairs []Pair
}

// ApplyToFiles applies every file's batch, resolving and checking all of
// them before writing any, so a bad anchor in the second file leaves the
// first as it was.
//
// It exists because one call could name one file and a change rarely does.
// Measured over this project's replay set, every `e2` failure on the fast
// tier was the model putting a test file's anchor in a call whose path was
// the source file, because the task needs both and the schema allowed one.
//
// The all-or-nothing guarantee holds across files for everything the tool
// can decide in advance, which is every matching failure. A write that
// fails partway through is an I/O error rather than a bad edit, and is
// reported naming the file it stopped at.
func ApplyToFiles(edits []FileEdit) (Batch, error) {
	if len(edits) == 0 {
		return Batch{}, ErrEmptyOldString
	}

	planned := make([]plannedWrite, 0, len(edits))

	for _, fe := range edits {
		p, err := planFile(fe)
		if err != nil {
			return Batch{}, err
		}

		planned = append(planned, p)
	}

	out := Batch{Changes: make([]tool.Change, 0, len(planned))}

	for _, p := range planned {
		if err := writeAtomic(p.path, p.mode, []byte(p.after)); err != nil {
			return Batch{}, fmt.Errorf("writing %s: %w", p.path, err)
		}

		out.Changes = append(out.Changes, tool.Change{
			Path:    p.path,
			Added:   p.added,
			Removed: p.removed,
			Ranges:  changedSpan(p.before, p.after),
		})
		out.Notes = append(out.Notes, p.notes...)
	}

	return out, nil
}

// Batch is what one call to ApplyToFiles produced.
type Batch struct {
	Changes []tool.Change
	// Notes is what the caller has to be told about how a match was
	// reached, empty when every anchor matched as it was sent.
	Notes []string
}

// plannedWrite is one file's resolved batch, held until every file's batch
// has resolved.
type plannedWrite struct {
	path    string
	before  string
	after   string
	notes   []string
	mode    os.FileMode
	added   int
	removed int
}

func planFile(fe FileEdit) (plannedWrite, error) {
	if len(fe.Pairs) == 0 {
		return plannedWrite{}, ErrEmptyOldString
	}

	info, err := os.Lstat(fe.Path)
	if err != nil {
		return plannedWrite{}, fmt.Errorf("stat %s: %w", fe.Path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return plannedWrite{}, fmt.Errorf("%s: %w", fe.Path, ErrSymlink)
	}

	data, err := os.ReadFile(fe.Path) // #nosec G304 -- path is the file this tool exists to edit
	if err != nil {
		return plannedWrite{}, fmt.Errorf("reading %s: %w", fe.Path, err)
	}

	before := string(data)

	out, err := applyAll(before, fe.Pairs)
	if err != nil {
		return plannedWrite{}, err
	}

	return plannedWrite{
		path: fe.Path, before: before, after: out.source, mode: info.Mode(),
		added: out.added, removed: out.removed, notes: out.notes,
	}, nil
}

// splices is one located pair: the byte span of before it replaces and the
// text that goes there.
type located struct {
	text    string
	note    string
	start   int
	end     int
	added   int
	removed int
	index   int
}

// applyAll resolves every pair against before and splices the accepted ones
// in from the end, so an earlier splice never moves a later one's offsets.
func applyAll(before string, pairs []Pair) (applied, error) {
	spans := make([]located, 0, len(pairs))

	for i, p := range pairs {
		span, err := locate(before, p)
		if err != nil {
			return applied{}, fmt.Errorf("edit %d of %d: %w", i+1, len(pairs), err)
		}

		span.index = i + 1
		spans = append(spans, span)
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var added, removed int

	for i := range spans {
		added += spans[i].added
		removed += spans[i].removed

		if i > 0 && spans[i].start < spans[i-1].end {
			return applied{}, &OverlapError{First: spans[i-1].index, Second: spans[i].index}
		}
	}

	var notes []string

	for i := range spans {
		if spans[i].note != "" {
			notes = append(notes, spans[i].note)
		}
	}

	source := before
	for i := len(spans) - 1; i >= 0; i-- {
		source = source[:spans[i].start] + spans[i].text + source[spans[i].end:]
	}

	return applied{source: source, notes: notes, added: added, removed: removed}, nil
}

// applied is a whole batch's outcome: the new text and what it moved.
type applied struct {
	source  string
	notes   []string
	added   int
	removed int
}

// locate resolves one pair against src by replacing it there and reading
// back the span that moved. It reuses Replace rather than re-implementing
// the exact-then-fuzzy match, so a batch and a single pair can never
// disagree about what an anchor means.
func locate(src string, p Pair) (located, error) {
	res, err := replaceWithRepair(src, p.OldString, p.NewString, p.All)
	if err != nil {
		return located{}, err
	}

	head := commonPrefix(src, res.Source)
	tail := commonSuffix(src[head:], res.Source[head:])

	return located{
		note:    res.Note,
		start:   head,
		end:     len(src) - tail,
		text:    res.Source[head : len(res.Source)-tail],
		added:   res.Added,
		removed: res.Removed,
	}, nil
}

func commonPrefix(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}

	return n
}

func commonSuffix(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}

	return n
}

// changedSpan is the one line range holding every difference between before
// and after, in after's line numbers, empty when nothing moved.
func changedSpan(before, after string) []tool.LineRange {
	old := strings.Split(before, "\n")
	fresh := strings.Split(after, "\n")

	head := 0
	for head < len(old) && head < len(fresh) && old[head] == fresh[head] {
		head++
	}

	if head == len(old) && head == len(fresh) {
		return nil
	}

	tail := 0
	for tail < len(old)-head && tail < len(fresh)-head &&
		old[len(old)-1-tail] == fresh[len(fresh)-1-tail] {
		tail++
	}

	return []tool.LineRange{{Start: head + 1, End: max(head+1, len(fresh)-tail)}}
}

// OverlapError reports two edits in one batch whose spans intersect. Which
// one should win is undecidable, so the batch is refused rather than
// resolved.
type OverlapError struct {
	First  int
	Second int
}

// Error implements error.
func (e *OverlapError) Error() string {
	return fmt.Sprintf("edits %d and %d change overlapping text, so which one applies is "+
		"undecidable; send them as one edit or as separate calls", e.First, e.Second)
}

// Summarize describes the whole difference between two versions of one
// file, for a caller that reached the second by some route other than a
// pair of strings. Formatting a file after an edit is that route: it can
// move every line below an added import, so the span the pairs reported no
// longer names where the file changed.
func Summarize(before, after string) (int, int, []tool.LineRange) {
	ranges := changedSpan(before, after)
	if len(ranges) == 0 {
		return 0, 0, nil
	}

	old := strings.Split(before, "\n")
	fresh := strings.Split(after, "\n")

	head := 0
	for head < len(old) && head < len(fresh) && old[head] == fresh[head] {
		head++
	}

	tail := 0
	for tail < len(old)-head && tail < len(fresh)-head &&
		old[len(old)-1-tail] == fresh[len(fresh)-1-tail] {
		tail++
	}

	return len(fresh) - head - tail, len(old) - head - tail, ranges
}
