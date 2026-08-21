package edit

import (
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// Pair is one replacement in a batch: the exact text to find and what
// replaces it.
type Pair struct {
	OldString string
	NewString string
}

// ApplyAllToFile applies every pair to path in order, reading and writing
// the file once. It is all or nothing: a pair that matches zero or several
// places fails the whole batch with that pair's own error and leaves the
// file untouched, so a half-applied edit is never a state the caller has to
// reason about.
//
// The reported Ranges are one span covering every line that moved, computed
// from the file's before and after rather than from each replacement, since
// a later edit shifts the line numbers an earlier one reported.
func ApplyAllToFile(path string, pairs []Pair) (tool.Change, error) {
	if len(pairs) == 0 {
		return tool.Change{}, ErrEmptyOldString
	}

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

	before := string(data)
	source := before

	var added, removed int

	for i, p := range pairs {
		result, err := Replace(source, p.OldString, p.NewString)
		if err != nil {
			return tool.Change{}, fmt.Errorf("edit %d of %d: %w", i+1, len(pairs), err)
		}

		source = result.Source
		added += result.Added
		removed += result.Removed
	}

	if err := writeAtomic(path, info.Mode(), []byte(source)); err != nil {
		return tool.Change{}, fmt.Errorf("writing %s: %w", path, err)
	}

	return tool.Change{
		Path:    path,
		Added:   added,
		Removed: removed,
		Ranges:  changedSpan(before, source),
	}, nil
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
