package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/stakes"
	"github.com/kyleking/wavez/internal/tool"
)

var strReplaceSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type: schemaTypeString,
		Description: "File path, relative to the project root, of an existing file. " +
			"A path outside the root is refused.",
	},
	"old_string": {
		Type: schemaTypeString,
		Description: "Exact text to replace. Must match exactly one location, or the call " +
			"fails with the matching line numbers. Copy it verbatim from a prior read rather " +
			"than retyping it.",
	},
	"new_string": {
		Type: schemaTypeString,
		Description: "Text that replaces old_string entirely. To insert lines before or " +
			"after existing code, repeat that code inside new_string, or it is deleted. " +
			"Must differ from old_string.",
	},
}, propPath, "old_string", "new_string")

// StrReplace edits an existing file by replacing one exact (or
// whitespace-fuzzy) occurrence of old_string with new_string, wrapping
// internal/edit. On success it reports file and line counts rather than the
// diff, per the Modifiers principle that the model needs the fact of the
// change, not to re-read it. On failure it passes internal/edit's message
// straight through, since a model corrects a bad anchor from that message
// alone.
type StrReplace struct {
	changes *stakes.ChangeSet
	root    string
}

// NewStrReplace builds a StrReplace tool scoped to root. A nil changes
// records nothing, so a caller that does not score its run omits it.
func NewStrReplace(root string, changes *stakes.ChangeSet) *StrReplace {
	return &StrReplace{root: root, changes: changes}
}

// Name implements tool.Tool.
func (*StrReplace) Name() string { return "str_replace" }

// Description implements tool.Tool.
func (*StrReplace) Description() string {
	return "Replace one exact occurrence of old_string with new_string in an existing file. " +
		"new_string replaces old_string entirely, so an insertion must repeat the surrounding " +
		"lines. Fails if old_string matches zero or more than one location; the error names the " +
		"line numbers so you can widen old_string to make it unique."
}

// Schema implements tool.Tool.
func (*StrReplace) Schema() json.RawMessage { return strReplaceSchema }

type strReplaceInput struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// Run implements tool.Tool.
func (s *StrReplace) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("str_replace: %w", err)
	}

	var in strReplaceInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	abs, err := resolvePath(s.root, in.Path)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	change, err := edit.ApplyToFile(abs, in.OldString, in.NewString)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	change.Path = in.Path

	s.changes.Record(stakes.Edit{Path: in.Path, Before: in.OldString, After: in.NewString})

	content := fmt.Sprintf("%s: +%d -%d lines", in.Path, change.Added, change.Removed)
	if len(change.Ranges) > 0 {
		content = fmt.Sprintf("%s (now lines %d-%d)", content, change.Ranges[0].Start, change.Ranges[0].End)
	}

	return tool.Result{Content: content, Changes: []tool.Change{change}}, nil
}
