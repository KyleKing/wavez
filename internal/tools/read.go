package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	maxReadLines = 500
	// A comma-separated list must not pull the whole tree into the window
	// in a single result.
	maxReadFiles = 10
	minLineNum   = 1
	// An omitted end_line means "to the end of the file".
	maxInt = int(^uint(0) >> 1)
)

// errBadLineRange reports a start_line/end_line pair naming no lines.
var errBadLineRange = errors.New("start_line and end_line must satisfy")

var readSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type: schemaTypeString,
		Description: "File path, relative to the project root, or several separated by commas " +
			"to read them in one call. A path may carry its own range as " +
			"\"file.go:120-180\", which is how one call reads a different range of each file.",
	},
	"start_line": {
		Type:        schemaTypeInteger,
		Description: "First line, inclusive. Omit both lines to read the whole file.",
	},
	"end_line": {
		Type:        schemaTypeInteger,
		Description: "Last line, inclusive. Omit it to read to the end of the file.",
	},
}, propPath)

// Read reads a whole file or a line range from it, refusing any path
// outside the project root.
//
// Every line carries its own number, `N<tab>line`. The number is what a
// str_replace anchor and a stack trace are both stated in, and without it a
// model re-reads the file through the shell to get one: 14 of 40 shell calls
// on a dogfood run were `awk`, `sed -n`, or `cat -n` over a file read had
// already returned. See lineNumbered, which keeps a numbered block from
// being handed back as file content.
//
// It always returns the lines it was asked for, even ones it has returned
// before. Answering a repeat read with a reference to the earlier one saves
// nothing: measured on a dogfood run, four of four references were followed
// immediately by a shell command reading the same file, which cost 21 KB of
// shell output and 19 extra turns to recover what the reference withheld.
// Keeping a repeated read out of the history is compaction's job, where
// DedupeToolReads replaces a byte-identical earlier result without the model
// ever being told no.
type Read struct {
	scope    *Scope
	registry *lang.Registry
	root     string
	deps     deps
}

// NewRead builds a Read tool scoped to root, reporting each file it reads
// to scope.
func NewRead(root string, scope *Scope, opts ...Option) *Read {
	return &Read{root: root, scope: scope, registry: lang.NewDefaultRegistry(), deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Read) Name() string { return "read" }

// Description implements tool.Tool.
func (*Read) Description() string {
	return "Read a file, or a 1-indexed inclusive line range of one, from the project. " +
		"Each line comes back as its line number, a tab, then the text; strip that prefix " +
		"before reusing a line as an edit anchor or as file content. " +
		"Prefer search to locate code and read only the range it names, since reading whole " +
		"files to find something spends the window on lines you will not use. " +
		"A long file comes back as an outline of its declarations with each one's range."
}

// Schema implements tool.Tool.
func (*Read) Schema() json.RawMessage { return readSchema }

type readInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// normalizeRange fills in an omitted end_line and rejects a range that
// cannot name any lines.
func (in *readInput) normalizeRange() error {
	if in.EndLine == 0 && in.StartLine >= minLineNum {
		in.EndLine = maxInt
	}

	if in.StartLine == 0 && in.EndLine == 0 {
		return nil
	}

	if in.StartLine < minLineNum || in.EndLine < in.StartLine {
		return fmt.Errorf(
			"%w: 1 <= start_line <= end_line, got start_line=%d end_line=%d",
			errBadLineRange, in.StartLine, in.EndLine)
	}

	return nil
}

// Run implements tool.Tool.
func (r *Read) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("read: %w", err)
	}

	var in readInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	paths := readPaths(input, propPath, in.Path)
	if len(paths) == 0 {
		return tool.Fail(tool.CauseBadInput, "path is required"), nil
	}
	if len(paths) > maxReadFiles {
		return tool.Fail(tool.CauseBadInput, "%d paths in one call, at most %d", len(paths), maxReadFiles), nil
	}
	if len(paths) > 1 && (in.StartLine != 0 || in.EndLine != 0) {
		return tool.Fail(tool.CauseBadInput, "start_line and end_line read one file; give each path "+
			"its own range as \"file.go:120-180\", or name one path"), nil
	}

	if err := in.normalizeRange(); err != nil {
		return tool.Fail(tool.CauseBadInput, "%v", err), nil
	}

	return r.readAll(paths, in.StartLine, in.EndLine), nil
}

func (r *Read) readAll(paths []string, start, end int) tool.Result {
	blocks := make([]string, 0, len(paths))
	for _, raw := range paths {
		p, pStart, pEnd, err := splitRange(raw)
		if err != nil {
			return tool.Fail(tool.CauseBadInput, "%v", err)
		}

		if pStart == 0 && pEnd == 0 {
			pStart, pEnd = start, end
		}

		block, failure := r.readOne(p, pStart, pEnd)
		if failure != nil {
			return *failure
		}

		blocks = append(blocks, block)
	}

	return tool.Result{Content: strings.Join(blocks, "\n\n")}
}

// readOne answers for one path, which is a directory listing, an outline, or
// a line range.
func (r *Read) readOne(p string, start, end int) (string, *tool.Result) {
	abs, err := resolvePath(r.root, r.deps.extraRoots, p)
	if err != nil {
		return "", failure(tool.CauseRefused, "%v", err)
	}

	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		names, listErr := dirEntries(abs)
		if listErr != nil {
			return "", failure(tool.CauseIO, "%v", listErr)
		}

		return fmt.Sprintf("%s is a directory holding:\n%s", p, strings.Join(names, "\n")), nil
	}

	data, err := os.ReadFile(abs) // #nosec G304 -- abs is resolved and root-checked above
	if err != nil {
		return "", failure(tool.CauseIO, "reading %s: %v", p, err)
	}

	if start == 0 && end == 0 {
		if brief := outline(r.registry, p, data); brief != "" {
			return brief, nil
		}
	}

	r.scope.Observe(abs)

	result := rangeResult(p, data, start, end)
	if result.IsError {
		return "", &result
	}

	return result.Content, nil
}

// rangeSuffix is a path's own line range, `file.go:120-180`, `file.go:120-`
// to the end, or `file.go:120` for the one line. Only a suffix that is
// entirely digits and dashes is a range, so a path holding a colon is still
// a path.
var rangeSuffix = regexp.MustCompile(`^(.*):(\d+)(-(\d*))?$`)

// splitRange separates a path from the range written on it. A path with no
// range comes back with zeros, which the call's own start_line and end_line
// then fill.
func splitRange(raw string) (string, int, int, error) {
	m := rangeSuffix.FindStringSubmatch(raw)
	if m == nil {
		return raw, 0, 0, nil
	}

	start, err := strconv.Atoi(m[2])
	if err != nil || start < minLineNum {
		return "", 0, 0, fmt.Errorf("%w: 1 <= start <= end, got %q", errBadLineRange, raw)
	}

	switch {
	case m[3] == "":
		return m[1], start, start, nil
	case m[4] == "":
		return m[1], start, maxInt, nil
	}

	end, err := strconv.Atoi(m[4])
	if err != nil || end < start {
		return "", 0, 0, fmt.Errorf("%w: 1 <= start <= end, got %q", errBadLineRange, raw)
	}

	return m[1], start, end, nil
}

// readPaths collects every path one call asked for under key: the
// comma-separated values, plus any repeated top-level occurrence of it. A model batches by
// repeating the key (`{"path":"a","path":"b"}`), which JSON decoding resolves
// to the last one, so honoring only that silently answers half the question:
// it was tried three times across two dogfood runs.
func readPaths(input json.RawMessage, key, decoded string) []string {
	raw := repeatedStringField(input, key)
	if len(raw) == 0 {
		raw = []string{decoded}
	}

	out := make([]string, 0, len(raw))
	for _, group := range raw {
		for _, p := range strings.Split(group, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}

	return out
}

// rangeResult renders the given inclusive line range of data (or the whole
// file when start and end are both 0) truncated to maxReadLines, noting how
// many lines were dropped.
func rangeResult(path string, data []byte, start, end int) tool.Result {
	lines := strings.Split(string(data), "\n")
	total := len(lines)

	if start == 0 && end == 0 {
		start, end = 1, total
	} else {
		if start > total {
			return tool.Fail(tool.CauseBadInput, "%s has %d lines, start_line %d is past the end", path, total, start)
		}

		end = min(end, total)
	}

	selected := lines[start-1 : end]

	truncated := len(selected) > maxReadLines
	dropped := len(selected) - maxReadLines
	if truncated {
		selected = selected[:maxReadLines]
	}

	body := numbered(selected, start)
	if truncated {
		body += fmt.Sprintf("\n... [%d of %d lines truncated] ...", dropped, end-start+1)
	}

	content := fmt.Sprintf("%s (lines %d-%d of %d):\n%s", path, start, end, total, body)
	if truncated {
		content += "\nRe-read with a narrower start_line/end_line to see the truncated lines."
	}

	return tool.Result{Content: content}
}

// numbered prefixes each line with its 1-indexed file line number,
// separated by a tab, which is the format `cat -n` and the awk one-liners a
// model reaches for both produce.
func numbered(lines []string, start int) string {
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d\t%s", start+i, line)
	}

	return b.String()
}

// dirEntries names what one directory holds, with a trailing slash on the
// subdirectories. A read of a directory answers with this rather than an
// error, because the error costs the turn and the listing was the next call
// anyway: two of one run's reads named a directory and each was followed by
// the list call that should have been the first.
func dirEntries(abs string) ([]string, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", filepath.Base(abs), err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	return names, nil
}
