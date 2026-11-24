// Package apply makes a set of text edits to several files in one step. It
// reads the edits in the shape a model writes reliably, the lines to replace
// and their replacement and no line numbers, and writes nothing unless
// every edit applies.
package apply

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The lines that frame an edit. A file line names the file the edits after
// it are for.
const (
	filePrefix = "=== "
	openMark   = "<<<<<<<"
	splitMark  = "======="
	closeMark  = ">>>>>>>"
)

const (
	filePerm = 0o644
	dirPerm  = 0o750
)

// ErrEdits is a set of edits that was not applied: one could not be read,
// names a file outside the root, or does not match its file exactly once.
var ErrEdits = errors.New("edits not applied")

// Edit replaces Old with New in File, a slash-separated path under the
// root. An empty Old appends New to the file, creating it when it is not
// there.
type Edit struct {
	File string
	Old  string
	New  string
}

// Parse reads edits from text. Lines outside an edit that are not a file
// line are an error, so that a block cut short is never taken for less than
// was meant.
func Parse(text string) ([]Edit, error) {
	var p parser

	for n, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if err := p.take(line); err != nil {
			return nil, fmt.Errorf("%w: line %d %w", ErrEdits, n+1, err)
		}
	}

	switch {
	case p.part != outside:
		return nil, fmt.Errorf("%w: the last edit has no %s line", ErrEdits, closeMark)
	case len(p.edits) == 0:
		return nil, fmt.Errorf("%w: no edit found", ErrEdits)
	default:
		return p.edits, nil
	}
}

// Where a line of the text falls.
const (
	outside = iota
	inOld
	inNew
)

var (
	errNoFile  = errors.New("opens an edit before any " + filePrefix + "path line")
	errOutside = errors.New("is outside an edit")
)

// parser reads edits one line at a time.
type parser struct {
	file  string
	edits []Edit
	old   []string
	repl  []string
	part  int
}

func (p *parser) take(line string) error {
	mark := strings.TrimRight(line, " \t\r")

	switch p.part {
	case inOld:
		if mark == splitMark {
			p.part = inNew
		} else {
			p.old = append(p.old, line)
		}
	case inNew:
		if mark == closeMark {
			p.edits = append(p.edits, Edit{File: p.file, Old: join(p.old), New: join(p.repl)})
			p.part = outside
		} else {
			p.repl = append(p.repl, line)
		}
	default:
		return p.between(line, mark)
	}

	return nil
}

// between takes a line that is not inside an edit.
func (p *parser) between(line, mark string) error {
	switch {
	case strings.HasPrefix(line, filePrefix):
		p.file = filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(line, filePrefix)))
	case mark == openMark && p.file == "":
		return errNoFile
	case mark == openMark:
		p.part, p.old, p.repl = inOld, nil, nil
	case strings.TrimSpace(line) != "":
		return fmt.Errorf("%w: %q", errOutside, line)
	}

	return nil
}

// join puts lines back together, each ended by a newline.
func join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	return strings.Join(lines, "\n") + "\n"
}

// Apply makes the edits under root and returns the files it wrote, sorted.
// Edits to one file apply in order, each to what the ones before it left.
// When any edit fails nothing is written, and the error says which and why.
func Apply(root string, edits []Edit) ([]string, error) {
	contents := map[string]string{}

	for i, edit := range edits {
		if !filepath.IsLocal(filepath.FromSlash(edit.File)) {
			return nil, fmt.Errorf("%w: edit %d names %q, which is not a path under the project",
				ErrEdits, i+1, edit.File)
		}

		body, known := contents[edit.File]
		if !known {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(edit.File))) //nolint:gosec // checked local
			if err != nil && (!errors.Is(err, os.ErrNotExist) || edit.Old != "") {
				return nil, fmt.Errorf("%w: edit %d: %w", ErrEdits, i+1, err)
			}

			body = string(raw)
		}

		next, err := replaced(body, edit)
		if err != nil {
			return nil, fmt.Errorf("%w: edit %d, in %s: %w", ErrEdits, i+1, edit.File, err)
		}

		contents[edit.File] = next
	}

	files := make([]string, 0, len(contents))
	for file := range contents {
		files = append(files, file)
	}

	slices.Sort(files)

	for _, file := range files {
		full := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), dirPerm); err != nil {
			return nil, fmt.Errorf("creating the directory of %s: %w", file, err)
		}

		if err := os.WriteFile(full, []byte(contents[file]), filePerm); err != nil {
			return nil, fmt.Errorf("writing %s: %w", file, err)
		}
	}

	return files, nil
}

var (
	errNoMatch   = errors.New("the lines to replace are not in the file")
	errAmbiguous = errors.New("the lines to replace are in the file more than once; include a line more of context")
)

// replaced is body with the edit made.
func replaced(body string, edit Edit) (string, error) {
	if edit.Old == "" {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}

		return body + edit.New, nil
	}

	old := edit.Old

	switch strings.Count(body, old) {
	case 1:
	case 0:
		// A file whose last line has no newline still holds that line.
		if old = strings.TrimSuffix(old, "\n"); !strings.HasSuffix(body, old) || strings.Count(body, old) != 1 {
			return "", fmt.Errorf("%w%s", errNoMatch, nearest(body, edit.Old))
		}
	default:
		return "", errAmbiguous
	}

	return strings.Replace(body, old, edit.New, 1), nil
}

// nearest says where the first line of old does appear, which is usually
// where white space or a stale line made the rest miss.
func nearest(body, old string) string {
	first := strings.TrimSpace(strings.SplitN(old, "\n", 2)[0]) //nolint:mnd // the first line and the rest
	if first == "" {
		return ""
	}

	for n, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == first {
			return fmt.Sprintf("; its first line is at line %d, so a later line or the indentation differs", n+1)
		}
	}

	return "; its first line is nowhere in the file"
}
