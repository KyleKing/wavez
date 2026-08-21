package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

const (
	maxReadLines = 500
	minLineNum   = 1
	// An omitted end_line means "to the end of the file".
	maxInt = int(^uint(0) >> 1)
)

var readSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type:        schemaTypeString,
		Description: "File path, relative to the project root. A path outside the root is refused.",
	},
	"start_line": {
		Type: schemaTypeInteger,
		Description: "1-indexed first line to read, inclusive. Omit both start_line and " +
			"end_line to read the whole file.",
	},
	"end_line": {
		Type: schemaTypeInteger,
		Description: "1-indexed last line to read, inclusive. Omit it to read from " +
			"start_line to the end of the file. Must be >= start_line when set.",
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
	scope *Scope
	root  string
}

// NewRead builds a Read tool scoped to root, reporting each file it reads
// to scope.
func NewRead(root string, scope *Scope) *Read {
	return &Read{root: root, scope: scope}
}

// Name implements tool.Tool.
func (*Read) Name() string { return "read" }

// Description implements tool.Tool.
func (*Read) Description() string {
	return "Read a file, or a 1-indexed inclusive line range of one, from the project. " +
		"Each line comes back as its line number, a tab, then the text; strip that prefix " +
		"before reusing a line as an edit anchor or as file content. " +
		"Prefer search to locate code and read only the range it names; reading whole files " +
		"to find something spends the context window on lines you will not use. " +
		"Refuses paths outside the project root."
}

// Schema implements tool.Tool.
func (*Read) Schema() json.RawMessage { return readSchema }

type readInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Run implements tool.Tool.
func (r *Read) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("read: %w", err)
	}

	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	abs, err := resolvePath(r.root, in.Path)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	if in.EndLine == 0 && in.StartLine >= minLineNum {
		in.EndLine = maxInt
	}

	if in.StartLine != 0 || in.EndLine != 0 {
		if in.StartLine < minLineNum || in.EndLine < in.StartLine {
			return tool.Errorf(
				"start_line and end_line must satisfy 1 <= start_line <= end_line, got start_line=%d end_line=%d",
				in.StartLine, in.EndLine,
			), nil
		}
	}

	data, err := os.ReadFile(abs) // #nosec G304 -- abs is resolved and root-checked above
	if err != nil {
		return tool.Errorf("reading %s: %v", in.Path, err), nil
	}

	r.scope.Observe(abs)

	return rangeResult(in.Path, data, in.StartLine, in.EndLine), nil
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
			return tool.Errorf("%s has %d lines, start_line %d is past the end", path, total, start)
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
