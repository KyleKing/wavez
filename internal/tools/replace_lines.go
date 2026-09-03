package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	propFrom = "from_line"
	propTo   = "to_line"
	// How many replaced lines come back in the result. The hash proves the
	// file has not moved and cannot prove the caller meant this range, so what
	// it took is shown rather than counted.
	replacedEcho = 6
)

var replaceLinesSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type:        schemaTypeString,
		Description: "File to edit, relative to the project root.",
	},
	propFrom: {
		Type:        schemaTypeInteger,
		Description: "First line to replace, numbered as read numbered it.",
	},
	propTo: {
		Type:        schemaTypeInteger,
		Description: "Last line to replace, included in the replacement.",
	},
	propNewString: {
		Type:        schemaTypeString,
		Description: "What those lines become. Empty deletes them.",
	},
}, propPath, propFrom, propTo, propNewString)

// ReplaceLines replaces a line range in a file the run has read, checking the
// file still holds the bytes it was shown.
//
// It is `str_replace` without the anchor. Measured on the 2026-09-02 ruff
// lane, `old_string` was 11,627 of the 40,863 bytes the run spent on edits,
// 28% of the payload, and every byte of it was a copy of text the file
// already held and the run had already been sent. Two integers say the same
// thing. What the anchor also did was prove the caller was looking at the
// current file, and the hash of the bytes `read` returned proves that
// instead, refusing rather than applying where the file has moved since.
//
// What neither proves is that the caller meant this range rather than one ten
// lines off, so the result carries the lines it took.
type ReplaceLines struct {
	scope *Scope
	root  string
	deps  deps
}

// NewReplaceLines builds a ReplaceLines tool scoped to root.
func NewReplaceLines(root string, scope *Scope, opts ...Option) *ReplaceLines {
	return &ReplaceLines{root: root, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*ReplaceLines) Name() string { return "replace_lines" }

// Description implements tool.Tool.
func (*ReplaceLines) Description() string {
	return "Replace a line range in a file you have read, addressed by the numbers read printed. " +
		"Prefer it over str_replace for a block you just read: no anchor to copy back."
}

// Schema implements tool.Tool.
func (*ReplaceLines) Schema() json.RawMessage { return replaceLinesSchema }

// Risk implements tool.Tool.
func (*ReplaceLines) Risk() tool.RiskClass { return tool.RiskWriteLocal }

type replaceLinesInput struct {
	Path      string `json:"path"`
	NewString string `json:"new_string"`
	From      int    `json:"from_line"`
	To        int    `json:"to_line"`
}

// Run implements tool.Tool.
func (r *ReplaceLines) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("replace_lines: %w", err)
	}

	var in replaceLinesInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if in.Path == "" || in.From <= 0 || in.To < in.From {
		return tool.Fail(tool.CauseBadInput,
			"replace_lines needs path and a line range with from_line >= 1 and to_line >= from_line"), nil
	}

	abs, err := resolvePath(r.root, r.deps.extraRoots, in.Path)
	if err != nil {
		return failWith(err), nil
	}

	if err := r.scope.Edit(abs); err != nil {
		return failWith(err), nil
	}

	release, err := r.deps.hold(ctx, abs)
	if err != nil {
		return failWith(err), nil
	}
	defer release()

	data, err := os.ReadFile(abs) //nolint:gosec // a path already resolved under the project root
	if err != nil {
		return failWith(fmt.Errorf("reading %s: %w", in.Path, err)), nil
	}

	if !r.deps.seen.Shown(ctx, abs, data) {
		return tool.Fail(tool.CauseConflict,
			"%s is not the file you were shown, so its line numbers mean something else now. "+
				"Read it again, or use str_replace, which anchors on text", in.Path), nil
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if in.To > len(lines) {
		return tool.Fail(tool.CauseBadInput,
			"%s has %d lines and to_line is %d", in.Path, len(lines), in.To), nil
	}

	return r.apply(ctx, abs, &in, lines, data)
}

func (r *ReplaceLines) apply(
	ctx context.Context, abs string, in *replaceLinesInput, lines []string, data []byte,
) (tool.Result, error) {
	text := in.NewString
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	change, err := edit.ApplySpansToFile(abs, []edit.Span{{
		Line: in.From - 1, EndLine: in.To, NewText: text,
	}})
	if err != nil {
		return failWith(err), nil
	}

	change.Path = in.Path
	r.scope.Wrote(abs)

	if after, rerr := os.ReadFile(abs); rerr == nil { //nolint:gosec // the path resolved above
		r.deps.seen.Note(ctx, abs, after)
	} else {
		r.deps.seen.Note(ctx, abs, data)
	}

	return tool.Result{
		Content: fmt.Sprintf("%s: +%d -%d lines, replacing %s",
			in.Path, change.Added, change.Removed, tookLines(lines, in.From, in.To)),
		Changes: []tool.Change{change},
	}, nil
}

// tookLines names what the range held, so a range that was off by ten is
// visible in the result rather than in a gate several turns on.
func tookLines(lines []string, from, to int) string {
	took := lines[from-1 : to]
	if len(took) > replacedEcho {
		return fmt.Sprintf("%d lines from %q to %q",
			len(took), strings.TrimSpace(took[0]), strings.TrimSpace(took[len(took)-1]))
	}

	trimmed := make([]string, 0, len(took))
	for _, line := range took {
		trimmed = append(trimmed, strings.TrimSpace(line))
	}

	return fmt.Sprintf("%q", strings.Join(trimmed, " / "))
}
