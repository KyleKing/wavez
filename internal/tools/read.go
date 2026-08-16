package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

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
// outside the project root. A second whole-file read of a file whose
// content has not changed since the first returns a short reference
// instead of the content again, so an unmodified file is never re-read
// into context. Reading a range never consults or updates that cache: a
// range read is a deliberate request for exactly those lines.
type Read struct {
	hashes map[string]string
	root   string
	mu     sync.Mutex
}

// NewRead builds a Read tool scoped to root.
func NewRead(root string) *Read {
	return &Read{root: root, hashes: make(map[string]string)}
}

// Name implements tool.Tool.
func (*Read) Name() string { return "read" }

// Description implements tool.Tool.
func (*Read) Description() string {
	return "Read a file, or a 1-indexed inclusive line range of one, from the project. " +
		"Refuses paths outside the project root. Re-reading an unchanged file returns a short " +
		"reference instead of the content, so do not re-read a file you have not edited."
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

	if in.StartLine == 0 && in.EndLine == 0 {
		return r.runWholeFile(in.Path, abs, data), nil
	}

	return rangeResult(in.Path, data, in.StartLine, in.EndLine), nil
}

func (r *Read) runWholeFile(path, abs string, data []byte) tool.Result {
	hash := contentHash(data)

	r.mu.Lock()
	prev, seen := r.hashes[abs]
	r.hashes[abs] = hash
	r.mu.Unlock()

	if seen && prev == hash {
		lines := strings.Count(string(data), "\n") + 1

		return tool.Result{Content: fmt.Sprintf(
			"%s is unchanged since your last full read (hash %s, %d lines). "+
				"Its content was already returned; re-read only if you expect it to have changed.",
			path, shortHash(hash), lines,
		)}
	}

	return rangeResult(path, data, 0, 0)
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

	truncated := false
	if len(selected) > maxReadLines {
		dropped := len(selected) - maxReadLines
		selected = selected[:maxReadLines]
		truncated = true

		selected = append(selected, fmt.Sprintf("... [%d of %d lines truncated] ...", dropped, end-start+1))
	}

	content := fmt.Sprintf("%s (lines %d-%d of %d):\n%s", path, start, end, total, strings.Join(selected, "\n"))
	if truncated {
		content += "\nRe-read with a narrower start_line/end_line to see the truncated lines."
	}

	return tool.Result{Content: content}
}
