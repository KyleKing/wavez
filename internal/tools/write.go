package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

var writeSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type: schemaTypeString,
		Description: "File path, relative to the project root, that must not already exist. " +
			"A path outside the root, or one that already exists, is refused; edit an " +
			"existing file with str_replace instead.",
	},
	"content": {
		Type:        schemaTypeString,
		Description: "Full text of the new file.",
	},
}, propPath, "content")

const (
	newFilePerm = 0o644
	// A file opening with a shebang is meant to be run, so write sets the
	// executable bit rather than making the model spend a failed execution
	// and a chmod on discovering it did not. Measured on qwen3:8b: writing
	// a script cost `./x.sh` exiting 126, a `chmod +x` through the
	// permission gate, and a re-run, three tool calls to run one script.
	// The guard reads a script's contents when something runs it, so the
	// bit costs no check that was doing work.
	execFilePerm = 0o755
)

// Write creates a new file with the given content. It refuses to overwrite
// a file that already exists (str_replace edits those) and refuses a path
// outside the project root.
type Write struct {
	scope *Scope
	root  string
}

// NewWrite builds a Write tool scoped to root, reporting each file it
// creates to scope.
func NewWrite(root string, scope *Scope) *Write {
	return &Write{root: root, scope: scope}
}

// permFor gives a file with a shebang the executable bit and every other
// file the ordinary one.
func permFor(content string) os.FileMode {
	if strings.HasPrefix(content, "#!") {
		return execFilePerm
	}

	return newFilePerm
}

// Name implements tool.Tool.
func (*Write) Name() string { return "write" }

// Description implements tool.Tool.
func (*Write) Description() string {
	return "Create a new file with the given content. Fails if the file already exists " +
		"(use str_replace to edit it) or if the path is outside the project root."
}

// Schema implements tool.Tool.
func (*Write) Schema() json.RawMessage { return writeSchema }

type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Run implements tool.Tool.
func (w *Write) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("write: %w", err)
	}

	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	abs, err := resolvePath(w.root, in.Path)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	if _, statErr := os.Lstat(abs); statErr == nil {
		return tool.Errorf("%s already exists; use str_replace to edit it", in.Path), nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return tool.Errorf("checking %s: %v", in.Path, statErr), nil
	}

	if err := os.WriteFile(abs, []byte(in.Content), permFor(in.Content)); err != nil {
		return tool.Errorf("writing %s: %v", in.Path, err), nil
	}

	w.scope.Observe(abs)

	lines := 0
	if in.Content != "" {
		lines = strings.Count(in.Content, "\n") + 1
	}

	change := tool.Change{Path: in.Path, Added: lines}
	if lines > 0 {
		change.Ranges = []tool.LineRange{{Start: 1, End: lines}}
	}

	return tool.Result{
		Content: fmt.Sprintf("%s: %d lines written", in.Path, lines),
		Changes: []tool.Change{change},
	}, nil
}
