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
		Type:        schemaTypeString,
		Description: "File path, relative to the project root, that must not already exist.",
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
	deps  deps
}

// NewWrite builds a Write tool scoped to root, reporting each file it
// creates to scope.
func NewWrite(root string, scope *Scope, opts ...Option) *Write {
	return &Write{root: root, scope: scope, deps: newDeps(opts)}
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
		"(use str_replace to edit it)."
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
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	abs, err := resolvePath(w.root, in.Path)
	if err != nil {
		return tool.Fail(tool.CauseRefused, "%v", err), nil
	}

	if err := w.scope.Protected(abs); err != nil {
		return tool.Fail(tool.CauseRefused, "%v", err), nil
	}

	release, err := w.deps.hold(ctx, abs)
	if err != nil {
		return tool.Fail(tool.CauseConflict, "%v", err), nil
	}
	defer release()

	if lineNumbered(in.Content) {
		return tool.Fail(tool.CauseBadInput,
			"content carries the line numbers read prefixed each line with; "+
				"write the file's own text, without the leading number and tab"), nil
	}

	if _, statErr := os.Lstat(abs); statErr == nil {
		return tool.Fail(tool.CauseRefused, "%s already exists; use str_replace to edit it", in.Path), nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return tool.Fail(tool.CauseIO, "checking %s: %v", in.Path, statErr), nil
	}

	if err := os.WriteFile(abs, []byte(in.Content), permFor(in.Content)); err != nil {
		return tool.Fail(tool.CauseIO, "writing %s: %v", in.Path, err), nil
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
