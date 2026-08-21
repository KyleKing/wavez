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
// outside the project root. Every read is remembered by content hash and by
// the lines it delivered, so a read of lines the model already holds from a
// file it has not changed returns a short reference instead of the content
// again. Measured on one dogfood run: 16 of 32 reads returned content
// already in the window, and only whole-file reads were deduplicated.
type Read struct {
	delivered map[string]*fileReads
	scope     *Scope
	root      string
	mu        sync.Mutex
}

// span is an inclusive 1-indexed line range that was actually returned to
// the model, after truncation.
type span struct {
	start int
	end   int
}

// fileReads is what one file has already given the model, valid only while
// hash still matches what is on disk.
type fileReads struct {
	hash  string
	spans []span
}

func (f *fileReads) covers(s span) bool {
	for _, have := range f.spans {
		if have.start <= s.start && s.end <= have.end {
			return true
		}
	}

	return false
}

// NewRead builds a Read tool scoped to root, reporting each file it reads
// to scope.
func NewRead(root string, scope *Scope) *Read {
	return &Read{root: root, scope: scope, delivered: make(map[string]*fileReads)}
}

// Name implements tool.Tool.
func (*Read) Name() string { return "read" }

// Description implements tool.Tool.
func (*Read) Description() string {
	return "Read a file, or a 1-indexed inclusive line range of one, from the project. " +
		"Prefer search to locate code and read only the range it names; reading whole files " +
		"to find something spends the context window on lines you will not use. " +
		"Refuses paths outside the project root. Re-reading lines of an unchanged file returns a " +
		"short reference instead of the content, so do not re-read what you have not edited."
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

	return r.deliver(in.Path, abs, data, in.StartLine, in.EndLine), nil
}

// deliver returns the requested lines, or a reference to them when the model
// already holds those lines of a file whose content has not changed since.
func (r *Read) deliver(path, abs string, data []byte, start, end int) tool.Result {
	result, got := rangeResult(path, data, start, end)
	if got == (span{}) {
		return result
	}

	hash := contentHash(data)

	r.mu.Lock()
	defer r.mu.Unlock()

	prev, seen := r.delivered[abs]
	if !seen || prev.hash != hash {
		r.delivered[abs] = &fileReads{hash: hash, spans: []span{got}}

		return result
	}

	if prev.covers(got) {
		return tool.Result{Content: fmt.Sprintf(
			"%s is unchanged since you read lines %d-%d of it (hash %s). "+
				"Those lines were already returned; re-read only if you expect them to have changed.",
			path, got.start, got.end, shortHash(hash),
		)}
	}

	prev.spans = append(prev.spans, got)

	return result
}

// rangeResult renders the given inclusive line range of data (or the whole
// file when start and end are both 0) truncated to maxReadLines, noting how
// many lines were dropped. The returned span is the lines actually
// delivered, and is the zero span when the result is an error.
func rangeResult(path string, data []byte, start, end int) (tool.Result, span) {
	lines := strings.Split(string(data), "\n")
	total := len(lines)

	if start == 0 && end == 0 {
		start, end = 1, total
	} else {
		if start > total {
			return tool.Errorf("%s has %d lines, start_line %d is past the end", path, total, start), span{}
		}

		end = min(end, total)
	}

	selected := lines[start-1 : end]
	delivered := span{start: start, end: end}

	truncated := false
	if len(selected) > maxReadLines {
		dropped := len(selected) - maxReadLines
		selected = selected[:maxReadLines]
		truncated = true
		delivered.end = start + maxReadLines - 1

		selected = append(selected, fmt.Sprintf("... [%d of %d lines truncated] ...", dropped, end-start+1))
	}

	content := fmt.Sprintf("%s (lines %d-%d of %d):\n%s", path, start, end, total, strings.Join(selected, "\n"))
	if truncated {
		content += "\nRe-read with a narrower start_line/end_line to see the truncated lines."
	}

	return tool.Result{Content: content}, delivered
}
