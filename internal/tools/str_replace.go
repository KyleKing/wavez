package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	propOldString = "old_string"
	propNewString = "new_string"
)

var strReplaceSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type: schemaTypeString,
		Description: "File path, relative to the project root, of an existing file. " +
			"A path outside the root is refused.",
	},
	propOldString: {
		Type: schemaTypeString,
		Description: "Exact text to replace. Must match exactly one location, or the call " +
			"fails with the matching line numbers. Copy it from a prior read rather than " +
			"retyping it, without the line number and tab read puts in front of each line, " +
			"and anchor on the shortest snippet that appears exactly once: a long anchor " +
			"fails more often than a short one.",
	},
	propNewString: {
		Type: schemaTypeString,
		Description: "Text that replaces old_string entirely. To insert lines before or " +
			"after existing code, repeat that code inside new_string, or it is deleted. " +
			"Must differ from old_string.",
	},
	"edits": {
		Type: schemaTypeArray,
		Description: "Several replacements in one file, applied in order, instead of " +
			"old_string and new_string. All of them land or none do. Prefer this over one " +
			"call per replacement when a file needs more than one change.",
		Items: &schemaItems{
			Type: schemaTypeObject,
			Properties: map[string]schemaProperty{
				propOldString: {Type: schemaTypeString, Description: "Exact text to replace, as above."},
				propNewString: {Type: schemaTypeString, Description: "Text that replaces it, as above."},
			},
			Required: []string{propOldString, propNewString},
		},
	},
}, propPath)

// StrReplace edits an existing file by replacing one exact (or
// whitespace-fuzzy) occurrence of old_string with new_string, wrapping
// internal/edit. On success it reports file and line counts rather than the
// diff, per the Modifiers principle that the model needs the fact of the
// change, not to re-read it. On failure it passes internal/edit's message
// straight through, since a model corrects a bad anchor from that message
// alone.
type StrReplace struct {
	scope *Scope
	deps  deps
	root  string
}

// NewStrReplace builds a StrReplace tool scoped to root, checking each edit
// against scope.
func NewStrReplace(root string, scope *Scope, opts ...Option) *StrReplace {
	return &StrReplace{root: root, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*StrReplace) Name() string { return "str_replace" }

// Description implements tool.Tool.
func (*StrReplace) Description() string {
	return "Replace one exact occurrence of old_string with new_string in an existing file, or " +
		"several at once with edits. new_string replaces old_string entirely, so an insertion " +
		"must repeat the surrounding lines. Fails if old_string matches zero or more than one " +
		"location; the error names the line numbers so you can widen old_string to make it unique."
}

// Schema implements tool.Tool.
func (*StrReplace) Schema() json.RawMessage { return strReplaceSchema }

type strReplaceInput struct {
	Path      string     `json:"path"`
	OldString string     `json:"old_string"`
	NewString string     `json:"new_string"`
	Edits     []editPair `json:"edits"`
}

type editPair struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// pairs is the replacements one call asked for, in order.
func (in *strReplaceInput) pairs() []edit.Pair {
	if len(in.Edits) == 0 {
		return []edit.Pair{{OldString: in.OldString, NewString: in.NewString}}
	}

	out := make([]edit.Pair, 0, len(in.Edits))
	for _, e := range in.Edits {
		out = append(out, edit.Pair{OldString: e.OldString, NewString: e.NewString})
	}

	return out
}

// Run implements tool.Tool.
func (s *StrReplace) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("str_replace: %w", err)
	}

	var in strReplaceInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Fail(tool.CauseBadInput, "invalid input: %v", err), nil
	}

	abs, err := resolvePath(s.root, in.Path)
	if err != nil {
		return failWith(err), nil
	}

	if err := s.scope.Edit(abs); err != nil {
		return failWith(err), nil
	}

	release, err := s.deps.hold(ctx, abs)
	if err != nil {
		return failWith(err), nil
	}
	defer release()

	if len(in.Edits) > 0 && (in.OldString != "" || in.NewString != "") {
		return tool.Fail(tool.CauseBadInput,
			"send either edits or one old_string/new_string pair, not both"), nil
	}

	change, err := edit.ApplyAllToFile(abs, in.pairs())
	if err != nil {
		if errors.Is(err, edit.ErrNotFound) && lineNumbered(in.OldString) {
			return tool.Fail(tool.CauseBadInput,
				"%v\n\nold_string still carries the line numbers read prefixed each "+
					"line with. Send the file's own text, without the leading number and tab.", err), nil
		}

		return failWith(err), nil
	}

	change.Path = in.Path

	content := fmt.Sprintf("%s: +%d -%d lines", in.Path, change.Added, change.Removed)
	if len(in.Edits) > 1 {
		content = fmt.Sprintf("%s across %d edits", content, len(in.Edits))
	}
	if len(change.Ranges) > 0 {
		content = fmt.Sprintf("%s (now lines %d-%d)", content, change.Ranges[0].Start, change.Ranges[0].End)
	}

	return tool.Result{Content: content, Changes: []tool.Change{change}}, nil
}
