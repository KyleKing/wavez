package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	newDirPerm   = 0o755
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

// Risk implements tool.Tool.
func (*Write) Risk() tool.RiskClass { return tool.RiskWriteLocal }

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

	abs, err := resolvePath(w.root, w.deps.extraRoots, in.Path)
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

	if refusal, ok := w.admitOverwrite(abs, in.Path); !ok {
		return refusal, nil
	}

	// A new file's directory may not exist yet, and a run that gets told
	// "no such file or directory" about the file it just named reaches for
	// `mkdir -p` and then keeps writing through shell heredocs, which costs
	// an approval per file. The parent is already inside the resolved root.
	if err := os.MkdirAll(filepath.Dir(abs), newDirPerm); err != nil {
		return tool.Fail(tool.CauseIO, "creating the directory for %s: %v", in.Path, err), nil
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

// admitOverwrite decides whether abs may be written over. Overwriting a file
// this run has read is a rewrite it can account for, and refusing it costs
// more than it saves: a run told to use str_replace on a file it wrote
// itself deletes the file through the shell and writes it again, which loses
// the checkpoint undo reaches the work through. Anything this run has not
// read stays refused, since that is the blind clobber the guard exists for.
func (w *Write) admitOverwrite(abs, shown string) (tool.Result, bool) {
	_, statErr := os.Lstat(abs)
	if errors.Is(statErr, os.ErrNotExist) {
		return tool.Result{}, true
	}

	if statErr != nil {
		return tool.Fail(tool.CauseIO, "checking %s: %v", shown, statErr), false
	}

	if w.scope == nil || !w.scope.Read(abs) {
		return tool.Fail(tool.CauseRefused,
			"%s already exists and this run has not read it; read it first, or use str_replace", shown), false
	}

	if err := w.scope.Edit(abs); err != nil {
		return failWith(err), false
	}

	return tool.Result{}, true
}
