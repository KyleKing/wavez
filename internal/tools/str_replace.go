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
	propEdits     = "edits"
	propNewString = "new_string"
	propOldString = "old_string"
)

var strReplacePathProp = schemaProperty{
	Type: schemaTypeString,
	Description: "File path, relative to the project root, of an existing file. " +
		"A path outside the root is refused.",
}

// strReplaceSchema offers the single-replacement and the several-at-once
// shapes as separate branches so that each one requires every field it
// needs. Stating them as one object with only path required let a local
// turn close the call after old_string and lose new_string entirely, which
// is what 52 of 52 fast-tier calls across this project's thread logs did.
var strReplaceSchema = buildOneOf(
	branch(map[string]schemaProperty{
		propPath: strReplacePathProp,
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
				"Must differ from old_string, and must be \"\" to delete old_string outright.",
		},
	}, propPath, propOldString, propNewString),
	branch(map[string]schemaProperty{
		propPath: strReplacePathProp,
		propEdits: {
			Type: schemaTypeArray,
			Description: "Several replacements in one file, applied in order. All of them land " +
				"or none do. Prefer this over one call per replacement when a file needs more " +
				"than one change.",
			Items: &schemaItems{
				Type: schemaTypeObject,
				Properties: map[string]schemaProperty{
					propOldString: {Type: schemaTypeString, Description: "Exact text to replace, as above."},
					propNewString: {Type: schemaTypeString, Description: "Text that replaces it, as above."},
				},
				Required: []string{propOldString, propNewString},
			},
		},
	}, propPath, propEdits),
)

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

// strReplaceInput reads new_string through a pointer so that an absent
// field is distinguishable from an empty one. They mean opposite things: an
// empty new_string deletes old_string, and an absent one is a call that was
// cut short before it said what to replace old_string with.
type strReplaceInput struct {
	NewString *string    `json:"new_string"`
	Path      string     `json:"path"`
	OldString string     `json:"old_string"`
	Edits     []editPair `json:"edits"`
}

type editPair struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// shapeError reports whether the call names a complete replacement, and
// what is wrong when it does not.
//
// An absent new_string is refused rather than read as an empty one. A call
// cut short after old_string arrives in exactly that shape, and treating it
// as a deletion turns a lost argument into a destructive edit reported as a
// success, which is what one logged run did to a README line.
func (in *strReplaceInput) shapeError() (string, bool) {
	// A call with no path at all is missing more than its replacement, and
	// naming the last absent field first sends the reader past the first
	// one. resolvePath names it.
	if in.Path == "" {
		return "", true
	}

	if len(in.Edits) > 0 {
		if in.OldString != "" || in.NewString != nil {
			return "send either edits or one old_string/new_string pair, not both", false
		}

		return "", true
	}

	if in.NewString == nil {
		return "new_string was absent, so this call says what to replace but not what to " +
			"replace it with, and nothing was edited. Send both fields in one object, and " +
			`send new_string as "" to delete old_string outright.`, false
	}

	return "", true
}

// pairs is the replacements one call asked for, in order.
func (in *strReplaceInput) pairs() []edit.Pair {
	if len(in.Edits) == 0 {
		return []edit.Pair{{OldString: in.OldString, NewString: *in.NewString}}
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

	if msg, ok := in.shapeError(); !ok {
		return tool.Fail(tool.CauseBadInput, "%s", msg), nil
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

	change, err := edit.ApplyAllToFile(abs, in.pairs())
	if err != nil {
		if errors.Is(err, edit.ErrNotFound) && lineNumbered(in.OldString) {
			return tool.Fail(tool.CauseBadInput,
				"%v\n\nold_string still carries the line numbers read prefixed each "+
					"line with. Send the file's own text, without the leading number and tab.", err), nil
		}

		if errors.Is(err, edit.ErrNotFound) && len(in.Edits) > 1 {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nEvery edit in one call applies to %s. An anchor belonging to another "+
					"file needs its own call with that path.", err, in.Path), nil
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
