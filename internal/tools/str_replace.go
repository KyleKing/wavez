package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	propEdits     = "edits"
	propNewString = "new_string"
	propOldString = "old_string"
)

var strReplacePathProp = schemaProperty{
	Type:        schemaTypeString,
	Description: "File path, relative to the project root, of an existing file.",
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
			Description: "Exact text to replace, copied from a prior read. Anchor on the " +
				"shortest snippet that appears exactly once.",
		},
		propNewString: {
			Type: schemaTypeString,
			Description: "Text that replaces old_string entirely. To insert lines before or " +
				"after existing code, repeat that code inside new_string, or it is deleted. " +
				"Send \"\" to delete old_string outright.",
		},
	}, propPath, propOldString, propNewString),
	branch(map[string]schemaProperty{
		propPath: strReplacePathProp,
		propEdits: {
			Type: schemaTypeArray,
			Description: "Several replacements in one call. All of them land or none do. Each " +
				"one may name its own path, so a change spanning two files is one call.",
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
		"must repeat the surrounding lines."
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
	// Path is the file this one edit applies to, empty for the call's own
	// path. A change rarely fits in one file and a call could only name
	// one, which is what every recorded `e2` failure was.
	Path string `json:"path,omitempty"`
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

		if len(in.dedupedEdits()) == 0 {
			return "every edit in this call replaces text with itself, so there is nothing " +
				"to apply. Send the text you want in new_string.", false
		}

		return in.conflictingEdit()
	}

	if in.NewString == nil {
		return "new_string was absent, so this call says what to replace but not what to " +
			"replace it with, and nothing was edited. Send both fields in one object, and " +
			`send new_string as "" to delete old_string outright.`, false
	}

	return "", true
}

// conflictingEdit reports a batch that names one anchor twice with two
// different replacements. Which one should win is unanswerable, so the call
// is refused rather than resolved.
//
// A pair repeated exactly is a different thing and is not refused: a fast
// turn that starts repeating itself emits the same edit several times, and
// applying it once is what the call asked for. Measured on `h6`, one run
// sent the same pair five times and the next sent it twice, and naming the
// repetition to the model did not stop it doing so.
func (in *strReplaceInput) conflictingEdit() (string, bool) {
	replacement := make(map[string]string, len(in.Edits))
	for i, e := range in.Edits {
		prior, seen := replacement[e.OldString]
		if seen && prior != e.NewString {
			return fmt.Sprintf("edit %d has the same old_string as an earlier edit but a "+
				"different new_string, so which one should apply is undecidable. Send one "+
				"replacement per anchor.", i+1), false
		}

		replacement[e.OldString] = e.NewString
	}

	return "", true
}

// dedupedEdits is the batch with any exactly repeated pair dropped and any
// edit that replaces text with itself removed, in the order the call named
// them.
//
// A no-op cannot change the file, so failing the whole batch over one
// throws away the edits that would have applied. Measured across the `h6`
// lanes, three runs sent one real edit alongside a second that replaced
// text with itself, and all three landed nothing.
func (in *strReplaceInput) dedupedEdits() []editPair {
	out := make([]editPair, 0, len(in.Edits))
	seen := make(map[editPair]bool, len(in.Edits))

	for _, e := range in.Edits {
		if seen[e] || e.OldString == e.NewString {
			continue
		}

		seen[e] = true

		out = append(out, e)
	}

	return out
}

// pairs is the replacements one call asked for, in order.
func (in *strReplaceInput) pairs() []edit.Pair {
	// A call with no path at all is refused by resolvePath before anything
	// reads this, and shapeError lets that one through so the missing field
	// named first is the first one missing.
	if len(in.Edits) == 0 {
		if in.NewString == nil {
			return nil
		}

		return []edit.Pair{{OldString: in.OldString, NewString: *in.NewString}}
	}

	deduped := in.dedupedEdits()

	out := make([]edit.Pair, 0, len(deduped))
	for _, e := range deduped {
		out = append(out, edit.Pair{OldString: e.OldString, NewString: e.NewString})
	}

	return out
}

// byFile groups the call's replacements by the file each one names, in the
// order the paths first appear, so a change spanning two files is one call
// and its report reads in the order it was written.
func (in *strReplaceInput) byFile() []pathGroup {
	if len(in.Edits) == 0 {
		return []pathGroup{{Path: in.Path, Pairs: in.pairs()}}
	}

	order := make([]string, 0, len(in.Edits))
	pairs := map[string][]edit.Pair{}

	for _, e := range in.dedupedEdits() {
		path := e.Path
		if path == "" {
			path = in.Path
		}

		if _, seen := pairs[path]; !seen {
			order = append(order, path)
		}

		pairs[path] = append(pairs[path], edit.Pair{OldString: e.OldString, NewString: e.NewString})
	}

	out := make([]pathGroup, 0, len(order))
	for _, path := range order {
		out = append(out, pathGroup{Path: path, Pairs: pairs[path]})
	}

	return out
}

// pathGroup is one file's share of a call.
type pathGroup struct {
	Path  string
	Pairs []edit.Pair
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

	groups := in.byFile()

	edits, release, failure := s.prepare(ctx, groups)
	if failure != nil {
		return *failure, nil
	}
	defer release()

	changes, err := edit.ApplyToFiles(edits)
	if err != nil {
		return s.failedEdit(&in, edits, err), nil
	}

	for i := range changes {
		changes[i].Path = groups[i].Path
		s.scope.Wrote(edits[i].Path)
	}

	return tool.Result{Content: describeChanges(changes, len(in.pairs())), Changes: changes}, nil
}

// prepare resolves and locks every file the call names, returning one
// release for all of them. Every lease is taken before any edit applies,
// because a batch spanning two files must not half-apply when the second
// file is held by another thread.
func (s *StrReplace) prepare(
	ctx context.Context, groups []pathGroup,
) ([]edit.FileEdit, func(), *tool.Result) {
	edits := make([]edit.FileEdit, 0, len(groups))
	held := make([]func(), 0, len(groups))

	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i]()
		}
	}

	for _, g := range groups {
		abs, err := resolvePath(s.root, g.Path)
		if err == nil {
			err = s.scope.Edit(abs)
		}

		var hold func()
		if err == nil {
			hold, err = s.deps.hold(ctx, abs)
		}

		if err != nil {
			release()
			failure := failWith(err)

			return nil, func() {}, &failure
		}

		held = append(held, hold)
		edits = append(edits, edit.FileEdit{Path: abs, Pairs: g.Pairs})
	}

	return edits, release, nil
}

// failedEdit explains a batch that did not apply, adding what the error
// alone cannot say.
func (s *StrReplace) failedEdit(in *strReplaceInput, edits []edit.FileEdit, err error) tool.Result {
	if errors.Is(err, edit.ErrNotFound) {
		if name, ok := declaredBy(in); ok {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nold_string is the whole declaration of %s. Send it to declare instead, "+
					"as symbol %q with the new source: declare needs no anchor, so nothing has to "+
					"match. Measured on this project, an anchor of a whole declaration fails far "+
					"more often than declare does.", err, name, name)
		}

		if stale := s.staleFiles(edits); stale != "" {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nYou have edited %s since you last read it, so an anchor taken from that "+
					"read no longer matches. Read it again before anchoring, or use declare to "+
					"write the whole declaration by name.", err, stale)
		}

		if unread := s.unreadFiles(edits); unread != "" {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nYou have not read %s this run. A search result is matched lines, "+
					"trimmed, not the file's text, so an anchor taken from one will not match. "+
					"Read the file first, or use declare to write the whole declaration by name.",
				err, unread)
		}
	}

	if errors.Is(err, edit.ErrNotFound) && lineNumbered(in.OldString) {
		return tool.Fail(tool.CauseBadInput,
			"%v\n\nold_string still carries the line numbers read prefixed each "+
				"line with. Send the file's own text, without the leading number and tab.", err)
	}

	if errors.Is(err, edit.ErrNotFound) && len(in.Edits) > 1 {
		return tool.Fail(tool.CauseNoMatch,
			"%v\n\nAn edit applies to %s unless it names its own path, and every anchor is "+
				"matched against the file as you last read it. Give the edit a path if it "+
				"belongs to another file.", err, in.Path)
	}

	return failWith(err)
}

// staleFiles names the files this call touches that the run has written
// since it last read them, empty when none has. It is the one thing the
// harness knows about a failed anchor that the model cannot: measured on
// `e2`, a run that had just edited a file spent its remaining turns
// guessing anchors against the version it remembered.
func (s *StrReplace) staleFiles(edits []edit.FileEdit) string {
	var stale []string

	for _, e := range edits {
		if s.scope.Stale(e.Path) {
			stale = append(stale, relativeTo(s.root, e.Path))
		}
	}

	return strings.Join(stale, ", ")
}

// declKeywords open a declaration whose whole text a caller may have used
// as an anchor.
var declKeywords = []string{"func ", "type ", "var ", "const "}

// declaredBy names the declaration old_string spans, when it spans one.
// Measured over six `e2` lanes, 17 of 19 failed anchors were a whole
// declaration and every one of them failed, while `declare` failed 2 of 22
// calls: the shape is the signal, and the tool that has no anchor is the
// one that works.
func declaredBy(in *strReplaceInput) (string, bool) {
	old := in.OldString
	if len(in.Edits) == 1 {
		old = in.Edits[0].OldString
	}

	head := strings.TrimSpace(firstLineOf(old))

	for _, kw := range declKeywords {
		if strings.HasPrefix(head, kw) {
			if name := declaredName(head[len(kw):]); name != "" {
				return name, true
			}
		}
	}

	return "", false
}

func isIdentRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}

	return s
}

// declaredName is the identifier a declaration head names, skipping a
// method receiver.
func declaredName(rest string) string {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "(") {
		if i := strings.IndexByte(rest, ')'); i >= 0 {
			rest = strings.TrimSpace(rest[i+1:])
		}
	}

	end := strings.IndexFunc(rest, func(r rune) bool { return !isIdentRune(r) })
	if end < 0 {
		end = len(rest)
	}

	return rest[:end]
}

// unreadFiles names the files this call anchors into that the run has
// never read. Measured over six `e2` lanes, `str_replace` failed 30 of 30
// calls while the runs made 2 reads and 36 searches between them: the
// anchors were being drawn from search results.
func (s *StrReplace) unreadFiles(edits []edit.FileEdit) string {
	var unread []string

	for _, e := range edits {
		if !s.scope.Read(e.Path) {
			unread = append(unread, relativeTo(s.root, e.Path))
		}
	}

	return strings.Join(unread, ", ")
}

// describeChanges reports what landed: file and line counts rather than the
// diff, per the Modifiers principle that the model needs the fact of the
// change and not to re-read it.
func describeChanges(changes []tool.Change, applied int) string {
	parts := make([]string, 0, len(changes))

	for i := range changes {
		part := fmt.Sprintf("%s: +%d -%d lines", changes[i].Path, changes[i].Added, changes[i].Removed)
		if len(changes[i].Ranges) > 0 {
			part = fmt.Sprintf("%s (now lines %d-%d)", part, changes[i].Ranges[0].Start, changes[i].Ranges[0].End)
		}

		parts = append(parts, part)
	}

	out := strings.Join(parts, "; ")
	if applied > 1 {
		out = fmt.Sprintf("%s across %d edits", out, applied)
	}

	return out
}
