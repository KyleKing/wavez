// Package apply makes a set of text edits to several files in one step. It
// reads the edits in the shape a model writes reliably, the lines to replace
// and their replacement and no line numbers, and writes nothing unless
// every edit applies.
package apply

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
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

// Fill puts the framing lines into text written with %OPEN%, %SPLIT%, and
// %CLOSE% in their place. Source that shows the format is written that way,
// because a line that is only one of them reads as an unresolved merge to
// every tool that looks for one.
func Fill(text string) string {
	return strings.NewReplacer("%OPEN%", openMark, "%SPLIT%", splitMark, "%CLOSE%", closeMark).Replace(text)
}

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
	errMarker  = errors.New("is a second " + splitMark + " line in one edit: text that holds a line " +
		"like that cannot be edited this way")
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
		// A second split line means a line of the file is itself a marker,
		// and where the lines to replace end can no longer be told.
		if mark == splitMark {
			return errMarker
		}

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
// A file is reached only inside root: a path that leaves it, by its own
// spelling or through a symbolic link on the way, is an edit that fails.
func Apply(root string, edits []Edit) ([]string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", root, err)
	}

	defer dir.Close() //nolint:errcheck // nothing was written through a handle left open

	var (
		contents = map[string]string{}
		before   = map[string]*string{}
		infos    = map[string]os.FileInfo{}
	)

	for i, edit := range edits {
		// One file spelled two ways is one file, or the second edit would
		// start from what is on disk and undo the first.
		file := path.Clean(edit.File)
		if !filepath.IsLocal(filepath.FromSlash(file)) {
			return nil, fmt.Errorf("%w: edit %d names %q, which is not a path under the project",
				ErrEdits, i+1, edit.File)
		}

		if _, known := contents[file]; !known {
			body, info, err := original(dir, file, edit.Old == "")
			if err == nil {
				err = distinct(infos, file, info)
			}

			if err != nil {
				return nil, fmt.Errorf("%w: edit %d: %w", ErrEdits, i+1, err)
			}

			contents[file], before[file] = "", body
			if body != nil {
				contents[file] = *body
			}
		}

		next, err := replaced(contents[file], edit)
		if err != nil {
			return nil, fmt.Errorf("%w: edit %d, in %s: %w", ErrEdits, i+1, file, err)
		}

		contents[file] = next
	}

	return written(dir, contents, before)
}

// errSameFile is two paths of one set of edits that name one file.
var errSameFile = errors.New("names a file an earlier edit names by another path")

// original reads a file as it is before the edits. A file that is not there
// is nil, and an error unless the edit may create it.
func original(dir *os.Root, file string, mayCreate bool) (*string, os.FileInfo, error) {
	raw, err := dir.ReadFile(filepath.FromSlash(file))
	if errors.Is(err, os.ErrNotExist) && mayCreate {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the error names the file and what went wrong
	}

	info, err := dir.Stat(filepath.FromSlash(file))
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // as above
	}

	body := string(raw)

	return &body, info, nil
}

// distinct refuses a file that an earlier edit reached by another path, as
// two spellings do where the file system folds case, and remembers this one.
func distinct(infos map[string]os.FileInfo, file string, info os.FileInfo) error {
	if info == nil {
		return nil
	}

	for other, seen := range infos {
		if os.SameFile(seen, info) {
			return fmt.Errorf("%q %w, %q", file, errSameFile, other)
		}
	}

	infos[file] = info

	return nil
}

// written writes the new contents, and when a write fails puts back what
// the earlier ones replaced, so that the edits are all made or none is.
func written(dir *os.Root, contents map[string]string, before map[string]*string) ([]string, error) {
	files := slices.Sorted(maps.Keys(contents))

	for at, file := range files {
		err := dir.MkdirAll(filepath.FromSlash(path.Dir(file)), dirPerm)
		if err == nil {
			err = dir.WriteFile(filepath.FromSlash(file), []byte(contents[file]), filePerm)
		}

		if err == nil {
			continue
		}

		for _, done := range files[:at] {
			restore(dir, done, before[done])
		}

		return nil, fmt.Errorf("%w: writing %s: %w", ErrEdits, file, err)
	}

	return files, nil
}

// restore puts a file back as it was, or removes one the edits created. It
// runs after a write already failed, and what it cannot undo stays written.
func restore(dir *os.Root, file string, body *string) {
	if body == nil {
		_ = dir.Remove(filepath.FromSlash(file)) //nolint:errcheck // see above

		return
	}

	_ = dir.WriteFile(filepath.FromSlash(file), []byte(*body), filePerm) //nolint:errcheck // see above
}

var (
	errNoMatch   = errors.New("the lines to replace are not in the file")
	errAmbiguous = errors.New("the lines to replace are in the file more than once; include a line more of context")
)

// replaced is body with the edit made. The lines to replace have to be
// whole lines of the file: text that only occurs inside a longer line, as an
// edit written without its indentation does, is not a match, since replacing
// it would leave the start of that line in front of the replacement.
func replaced(body string, edit Edit) (string, error) {
	if edit.Old == "" {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}

		return body + edit.New, nil
	}

	old := edit.Old

	at := lineStarts(body, old)
	if len(at) == 0 {
		// A file whose last line has no newline still holds that line.
		old = strings.TrimSuffix(old, "\n")
		if strings.HasSuffix(body, old) {
			at = lineStarts(body[len(body)-len(old):], old)
			for i := range at {
				at[i] += len(body) - len(old)
			}

			if start := len(body) - len(old); start > 0 && body[start-1] != '\n' {
				at = nil
			}
		}
	}

	switch len(at) {
	case 1:
		return body[:at[0]] + edit.New + body[at[0]+len(old):], nil
	case 0:
		return "", fmt.Errorf("%w%s", errNoMatch, nearest(body, edit.Old))
	default:
		return "", errAmbiguous
	}
}

// lineStarts returns where old occurs in body at the start of a line.
func lineStarts(body, old string) []int {
	var at []int

	for from := 0; ; {
		i := strings.Index(body[from:], old)
		if i < 0 {
			return at
		}

		if start := from + i; start == 0 || body[start-1] == '\n' {
			at = append(at, start)
		}

		from += i + 1
	}
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
