package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kyleking/wavez/internal/tool"
)

// ErrNotEdited reports an undo of a file this run never wrote.
var ErrNotEdited = errors.New("this run has not edited that file")

var undoSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type:        schemaTypeString,
		Description: "The file to put back, relative to the project root.",
	},
}, "path")

// Undo puts a file back the way this run found it.
//
// It exists because a run that edits itself into a corner has no way out.
// One recorded `h7` lane spent 44 turns and reached its deadline after
// three shell attempts at `git checkout -- <file>` and `jj checkout --
// <file>`, each refused by the guard, because reverting through version
// control is a write, and every version-control write is off the shell.
//
// This reaches version control not at all. It restores from the bytes the
// run itself snapshotted before its first edit, so the worst it can discard
// is work the same run made, and a file the run never touched is not
// something it can reach.
type Undo struct {
	scope *Scope
	root  string
	deps  deps
}

// NewUndo builds an Undo rooted at root over the run's own scope.
func NewUndo(root string, scope *Scope, opts ...Option) *Undo {
	return &Undo{root: root, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Undo) Name() string { return "undo" }

// Description implements tool.Tool.
func (*Undo) Description() string {
	return "Discard every edit this run made to one file. Only reaches files this run edited."
}

// Schema implements tool.Tool.
func (*Undo) Schema() json.RawMessage { return undoSchema }

type undoInput struct {
	Path string `json:"path"`
}

// Run implements tool.Tool.
func (u *Undo) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("undo: %w", err)
	}

	var in undoInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	abs, err := resolvePath(u.root, in.Path)
	if err != nil {
		return tool.Fail(tool.CauseRefused, "%v", err), nil
	}

	src, existed, edited := u.scope.Origin(abs)
	if !edited {
		return tool.Fail(tool.CauseNoMatch, "%v: %s", ErrNotEdited, in.Path), nil
	}

	release, err := u.deps.hold(ctx, abs)
	if err != nil {
		return tool.Fail(tool.CauseConflict, "%v", err), nil
	}
	defer release()

	change, err := restoreOrigin(abs, in.Path, src, existed)
	if err != nil {
		return tool.Fail(tool.CauseIO, "%v", err), nil
	}

	u.scope.Wrote(abs)

	return tool.Result{Content: describeUndo(in.Path, existed), Changes: []tool.Change{change}}, nil
}

// restoreOrigin writes src back, or removes the file when this run
// created it.
func restoreOrigin(abs, path string, src []byte, existed bool) (tool.Change, error) {
	if !existed {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return tool.Change{}, fmt.Errorf("removing %s: %w", path, err)
		}

		return tool.Change{Path: path}, nil
	}

	if err := os.WriteFile(abs, src, permFor(string(src))); err != nil {
		return tool.Change{}, fmt.Errorf("writing %s: %w", path, err)
	}

	return tool.Change{Path: path}, nil
}

func describeUndo(path string, existed bool) string {
	if !existed {
		return path + ": removed, because this run created it"
	}

	return path + ": put back the way this run found it"
}
