package tui

import (
	"strconv"
	"strings"
)

// diffKind classifies one rendered diff line, so the pane can style it and
// Ask-a-line can tell an anchor from a header.
type diffKind int

const (
	diffFile diffKind = iota
	diffHunk
	diffAdd
	diffRemove
	diffContext
)

// diffRow is one line of a rendered diff, carrying enough to anchor a
// question to it: the file it belongs to and its line number in the file as
// it is now. Removed lines have no such number and report zero.
type diffRow struct {
	Text string
	File string
	Line int
	Kind diffKind
}

// stateDirPrefix is wavez's own per-project directory. Its gate log and
// index change on every run, so it appears in a thread's diff as work the
// thread did, which it is not.
const stateDirPrefix = ".wavez/"

// parseDiff turns git-format unified diff text into rows. It keeps context
// lines because a question about a change is usually a question about the
// code around it, and it tracks post-image line numbers so a row names a
// place a reader can go to.
func parseDiff(unified string) []diffRow {
	var (
		out     []diffRow
		file    string
		line    int
		skipped bool
	)

	for _, raw := range strings.Split(unified, "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "), strings.HasPrefix(raw, "index "),
			strings.HasPrefix(raw, "--- "), strings.HasPrefix(raw, "new file"),
			strings.HasPrefix(raw, "deleted file"), strings.HasPrefix(raw, "similarity"),
			strings.HasPrefix(raw, "rename "), raw == "":
			continue
		case strings.HasPrefix(raw, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(raw, "+++ "), "b/")
			skipped = strings.HasPrefix(file, stateDirPrefix)

			if skipped {
				continue
			}

			out = append(out, diffRow{Kind: diffFile, File: file, Text: file})
		case skipped:
			continue
		case strings.HasPrefix(raw, "@@"):
			line = hunkStart(raw)
			out = append(out, diffRow{Kind: diffHunk, File: file, Line: line, Text: raw})
		case strings.HasPrefix(raw, "+"):
			out = append(out, diffRow{Kind: diffAdd, File: file, Line: line, Text: raw})
			line++
		case strings.HasPrefix(raw, "-"):
			out = append(out, diffRow{Kind: diffRemove, File: file, Text: raw})
		default:
			out = append(out, diffRow{Kind: diffContext, File: file, Line: line, Text: raw})
			line++
		}
	}

	return out
}

// hunkStart reads the post-image start line out of a hunk header, returning
// zero when the header is not one this parser understands.
func hunkStart(header string) int {
	plus := strings.Index(header, "+")
	if plus < 0 {
		return 0
	}

	rest := header[plus+1:]

	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return 0
	}

	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}

	return n
}

// anchor renders where a diff row points, for the prompt Ask-a-line builds.
// A row with no post-image line names the file alone.
func (r diffRow) anchor() string {
	if r.File == "" {
		return ""
	}

	if r.Line == 0 {
		return r.File
	}

	return r.File + ":" + strconv.Itoa(r.Line)
}
