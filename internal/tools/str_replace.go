package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/gofix"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	editedFilePerm = 0o644

	// A rewrite bigger than this is cheaper to read than to echo.
	maxEchoedLines = 40

	propEdits      = "edits"
	propNewString  = "new_string"
	propOldString  = "old_string"
	propReplaceAll = "replace_all"
)

// replaceAllProp is the way out of an ambiguity the file cannot resolve.
// Refusing every anchor that matches twice asks the caller to widen it, and
// a rename's call sites are written identically, so no widening exists: one
// lane sent the same pair five times and died stagnant against that answer.
// It stays optional and off, so the wider edit is only ever the one asked
// for.
var replaceAllProp = schemaProperty{
	Type:        schemaTypeBoolean,
	Description: "Replace every occurrence rather than requiring exactly one.",
}

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
			Description: "What old_string becomes, replacing it entirely: to insert around " +
				"existing code, repeat that code here or it is deleted. \"\" deletes it.",
		},
		propReplaceAll: replaceAllProp,
	}, propPath, propOldString, propNewString, propReplaceAll),
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
					propPath: {
						Type:        schemaTypeString,
						Description: "File this edit applies to. Omit to use the call's own path.",
					},
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
	root  string
	deps  deps
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
	return "Replace text in an existing file: one occurrence of old_string with new_string, or " +
		"several at once with edits."
}

// Schema implements tool.Tool.
func (*StrReplace) Schema() json.RawMessage { return strReplaceSchema }

// strReplaceInput reads new_string through a pointer so that an absent
// field is distinguishable from an empty one. They mean opposite things: an
// empty new_string deletes old_string, and an absent one is a call that was
// cut short before it said what to replace old_string with.
type strReplaceInput struct {
	NewString *string `json:"new_string"`
	Path      string  `json:"path"`
	OldString string  `json:"old_string"`
	// Source is not this tool's field. It is declare's, and a call carrying
	// it is a declare call sent here, which one lane made three times and
	// died stagnant on a message about new_string.
	Source string `json:"source"`
	// Content is write's field, naming a whole file's text. A call carrying
	// it wants to overwrite the file rather than edit part of it, which one
	// lane asked for three times and died stagnant on a message about
	// new_string.
	Content    string     `json:"content"`
	Edits      []editPair `json:"edits"`
	ReplaceAll bool       `json:"replace_all"`
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

	if in.Content != "" && in.OldString == "" && in.NewString == nil && len(in.Edits) == 0 {
		return "content is write's field, not this tool's, and names a whole file rather than " +
			"the part that changes, so nothing was edited. Send old_string and new_string for " +
			"the lines that differ. write takes path and content, and only for a file that " +
			"does not exist yet.", false
	}

	if in.Source != "" && in.OldString == "" && in.NewString == nil && len(in.Edits) == 0 {
		return "source is declare's field, not this tool's, so nothing was edited. Send this " +
			"call to declare with symbol and source to write the whole declaration by name, or " +
			"send old_string and new_string here.", false
	}

	if len(in.Edits) > 0 {
		if in.OldString != "" || in.NewString != nil {
			return "send either edits or one old_string/new_string pair, not both", false
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
		if in.NewString == nil || in.OldString == *in.NewString {
			return nil
		}

		return []edit.Pair{{OldString: in.OldString, NewString: *in.NewString, All: in.ReplaceAll}}
	}

	deduped := in.dedupedEdits()

	out := make([]edit.Pair, 0, len(deduped))
	for _, e := range deduped {
		out = append(out, edit.Pair{OldString: e.OldString, NewString: e.NewString, All: in.ReplaceAll})
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

		pairs[path] = append(pairs[path],
			edit.Pair{OldString: e.OldString, NewString: e.NewString, All: in.ReplaceAll})
	}

	if len(order) == 0 {
		// Every edit was a no-op, and the call still names a file: the
		// answer to it depends on what that file holds, so it has to be
		// resolved and read like any other.
		return []pathGroup{{Path: in.Path}}
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
	if err := decodeInput(input, &in); err != nil {
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

	if len(in.pairs()) == 0 {
		return nothingToApply(in.anchor(), edits), nil
	}

	before := sourceBefore(edits)

	batch, err := edit.ApplyToFiles(edits)
	if err != nil {
		return s.failedEdit(ctx, &in, edits, err), nil
	}

	changes := batch.Changes
	for i := range changes {
		changes[i].Path = groups[i].Path
		s.scope.Wrote(edits[i].Path)
	}

	reformatted := formatEdited(groups, edits, before, changes)

	parts := append([]string{describeChanges(changes, len(in.pairs()))}, dedupeNotes(batch.Notes)...)
	if reformatted != "" {
		parts = append(parts, reformatted)
	}
	if broke := brokenSyntax(groups, edits, before); broke != "" {
		parts = append(parts, broke)
	}

	content := strings.Join(parts, "\n")

	return tool.Result{Content: content, Changes: changes}, nil
}

// dedupeNotes keeps the first of each distinct note, since one batch can
// reach the same repair on every pair and saying so once is the report.
func dedupeNotes(notes []string) []string {
	out := make([]string, 0, len(notes))
	seen := make(map[string]bool, len(notes))

	for _, n := range notes {
		if n == "" || seen[n] {
			continue
		}

		seen[n] = true

		out = append(out, n)
	}

	return out
}

// sourceBefore reads each file a batch is about to edit. A file it cannot
// read yields nil, which reports nothing rather than a break it cannot
// attribute.
func sourceBefore(edits []edit.FileEdit) [][]byte {
	out := make([][]byte, len(edits))

	for i, e := range edits {
		src, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}

		out[i] = src
	}

	return out
}

// brokenSyntax names the files this batch left unparsable that parsed
// before it. The edit has already applied, so this warns on a successful
// result rather than failing.
func brokenSyntax(groups []pathGroup, edits []edit.FileEdit, before [][]byte) string {
	var lines []string

	for i, e := range edits {
		if before[i] == nil {
			continue
		}

		after, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}

		if msg, broke := gofix.BrokeSyntax(groups[i].Path, before[i], after); broke {
			lines = append(lines, msg)
		}
	}

	if len(lines) == 0 {
		return ""
	}

	return "The edit applied and left the file unparsable, which it was not before:\n  " +
		strings.Join(lines, "\n  ") +
		"\nRead the file and repair the structure rather than editing again from what you remember."
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

// anchor is the text this call names, whichever shape it came in.
func (in *strReplaceInput) anchor() string {
	if len(in.Edits) > 0 {
		return in.Edits[0].OldString
	}

	return in.OldString
}

// replacement is the text the anchor becomes, empty for a call that named
// no replacement at all.
func (in *strReplaceInput) replacement() string {
	if len(in.Edits) > 0 {
		return in.Edits[0].NewString
	}

	if in.NewString == nil {
		return ""
	}

	return *in.NewString
}

// nothingToApply answers a call whose every edit replaces text with itself.
//
// Where the file already holds that text, the state the call asked for is
// the state the file is in, and the call succeeded having changed nothing.
// Reporting it as a failure is what sent one run round the same block three
// times, and the batch path had always disagreed with the single-pair path
// about it: a no-op pair beside a real one is dropped, and a no-op pair
// alone was an error. Only where the text is absent is the call a mistake,
// and then it is the fields the wrong way round.
func nothingToApply(anchor string, edits []edit.FileEdit) tool.Result {
	for _, src := range sourceBefore(edits) {
		if src != nil && edit.Holds(string(src), anchor) {
			return tool.Result{Content: "no change: the file already holds exactly that text, " +
				"so there was nothing to replace. Nothing was written."}
		}
	}

	return tool.Fail(tool.CauseBadInput,
		"old_string and new_string are identical and the file does not hold that text, so it "+
			"is the replacement rather than the anchor: send the text it should replace as "+
			"old_string.")
}

// noChangeAdvice separates the two ways a call sends one text twice. It is
// the largest single failure str_replace records, and the error alone says
// only that the two fields matched. Neither branch tells the run its work
// may be done: one h11 run reached this while its file would not parse, and
// a call whose two halves collapsed is no evidence about the file's state.
func noChangeAdvice(edits []edit.FileEdit, anchor string) string {
	for _, src := range sourceBefore(edits) {
		if src != nil && strings.Contains(string(src), anchor) {
			return "The file already holds exactly that text, so this call would change " +
				"nothing. Read the file back and send its current text as old_string with " +
				"your change in new_string."
		}
	}

	return "old_string is the text to find and new_string is what to put in its place. What " +
		"you sent is not in the file, so it is the replacement: send the text it should " +
		"replace as old_string."
}

// failedEdit explains a batch that did not apply, adding what the error
// alone cannot say.
func (s *StrReplace) failedEdit(
	ctx context.Context, in *strReplaceInput, edits []edit.FileEdit, err error,
) tool.Result {
	if errors.Is(err, edit.ErrNotFound) {
		if name, ok := declaredBy(in); ok {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nold_string is the whole declaration of %s. Send it to declare instead, "+
					"as symbol %q with the new source: declare needs no anchor, so nothing has to "+
					"match. Measured on this project, an anchor of a whole declaration fails far "+
					"more often than declare does.", err, name, name)
		}

		if applied := s.appliedFiles(edits); applied != "" {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nThe text in new_string is already in %s and old_string is not, so this "+
					"edit has been made. Check the rest of the change rather than re-sending "+
					"this one.", err, applied)
		}

		if stale := s.staleFiles(edits); stale != "" {
			return staleAnchor(edits, in.anchor(), err, stale)
		}

		if unread := s.unreadFiles(edits); unread != "" {
			return tool.Fail(tool.CauseNoMatch,
				"%v\n\nYou have not read %s this run. A search result is matched lines, "+
					"trimmed, not the file's text, so an anchor taken from one will not match. "+
					"Read the file first, or use declare to write the whole declaration by name.",
				err, unread)
		}
	}

	var notUnique *edit.NotUniqueError
	if errors.As(err, &notUnique) && !in.ReplaceAll {
		return tool.Fail(tool.CauseAmbiguous,
			"%v\n\nWhere the %d sites are written identically no widening tells them apart. "+
				"Send the same call with replace_all: true to change all %d, or anchor on "+
				"surrounding text that differs.%s",
			err, len(notUnique.Lines), len(notUnique.Lines),
			s.orRename(ctx, in.anchor(), in.replacement()))
	}

	if errors.Is(err, edit.ErrNoChange) {
		return tool.Fail(tool.CauseBadInput, "%v\n\n%s", err, noChangeAdvice(edits, in.OldString))
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

// orRename points a pair that substitutes one identifier at the tool built
// for it. `ambiguous` went from 1 across the first corpus to 20, and every
// recorded case is a rename asked as a text edit: `h3` sent
// `bench.Read(path)` against four sites written identically, took the
// per-site path, and spent 57 turns on `sed`. Nothing else steers a rename
// task at `rename`.
//
// It says nothing once the index declares the new name, because `rename`
// starts from the declaration and a declaration already carrying the new
// name is one `rename` can no longer resolve. Two `h3` lanes hand-edited
// the declaration, met this refusal at the call sites, and spent their last
// two turns on a `rename` that answered that nothing is indexed under the
// old name. Asking only whether the old name is declared does not separate
// them, since `Read` is declared in three packages here and the rename
// touches one.
func (s *StrReplace) orRename(ctx context.Context, oldString, newString string) string {
	from, to, ok := substitutedIdentifier(oldString, newString)
	if !ok || !s.declares(ctx, from) || s.declares(ctx, to) {
		return ""
	}

	return fmt.Sprintf(" Every site here changes %s to %s and nothing else, and %s is a "+
		"declared symbol, so rename with symbol: %q, to: %q changes every site in the "+
		"project in one call, including the ones this file does not hold.",
		from, to, from, from, to)
}

// declares reports whether the index holds a declaration under name. Where
// no index is wired the answer is no, so advice that depends on one stays
// out of a build that cannot check it.
func (s *StrReplace) declares(ctx context.Context, name string) bool {
	if s.deps.symbols == nil {
		return false
	}

	results, _, err := searchWidening(ctx, s.deps.symbols, name)
	if err != nil {
		return false
	}

	for i := range results {
		if sym := results[i].Symbol; sym != nil && sym.Name == name {
			return true
		}
	}

	return false
}

// substitutedIdentifier reports the one identifier a pair replaces, where
// the two sides hold the same identifiers in the same order but for one.
// Anything else is an edit rather than a rename, including a pair that
// changes two names or reorders them.
func substitutedIdentifier(oldString, newString string) (string, string, bool) {
	before, after := identifiers(oldString), identifiers(newString)
	if len(before) != len(after) {
		return "", "", false
	}

	from, to := "", ""

	for i := range before {
		if before[i] == after[i] {
			continue
		}

		if from != "" {
			return "", "", false
		}

		from, to = before[i], after[i]
	}

	return from, to, from != "" && to != ""
}

// identifiers splits text on everything that cannot appear in a name,
// dropping the runs that open with a digit so a number is not read as one.
func identifiers(text string) []string {
	var out []string

	start := -1

	for i := 0; i <= len(text); i++ {
		if i < len(text) && isNameByte(text[i]) {
			if start < 0 {
				start = i
			}

			continue
		}

		if start >= 0 {
			if word := text[start:i]; word[0] < '0' || word[0] > '9' {
				out = append(out, word)
			}

			start = -1
		}
	}

	return out
}

// appliedFiles names the files whose text already holds an edit's
// replacement while holding no anchor for it. That is what a part-finished
// rename leaves behind, and the near-match report answers it with the suffix
// the anchor and the replacement share ("you sent: (path) / source has:
// Log(path)"), which reads as a typo rather than as work already done. One
// h3 run re-sent the same anchor against it.
func (s *StrReplace) appliedFiles(edits []edit.FileEdit) string {
	var done []string

	sources := sourceBefore(edits)

	for i := range edits {
		if sources[i] == nil {
			continue
		}

		for _, p := range edits[i].Pairs {
			if p.NewString == "" || edit.Holds(string(sources[i]), p.OldString) {
				continue
			}

			if edit.Holds(string(sources[i]), p.NewString) {
				done = append(done, relativeTo(s.root, edits[i].Path))

				break
			}
		}
	}

	return strings.Join(done, ", ")
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

// formatEdited runs the formatter over each Go file this call changed and
// names the files it rewrote, restating their change stats from the text
// that is now on disk.
//
// The format gate would do this a moment later, out of the caller's sight,
// and 15 of 18 anchor misses recorded here were against a file the same run
// had already changed. Formatting inside the call closes that window: what
// the result describes is what the next anchor will be matched against.
func formatEdited(groups []pathGroup, edits []edit.FileEdit, before [][]byte, changes []tool.Change) string {
	var touched []string

	for i := range edits {
		src, err := os.ReadFile(edits[i].Path)
		if err != nil {
			continue
		}

		out, changed := gofix.Format(edits[i].Path, src)
		if !changed {
			continue
		}

		//nolint:gosec // the path is one this call just edited through Scope
		if err := os.WriteFile(edits[i].Path, out, editedFilePerm); err != nil {
			continue
		}

		touched = append(touched, groups[i].Path)

		if before[i] != nil {
			added, removed, ranges := edit.Summarize(string(before[i]), string(out))
			changes[i].Added, changes[i].Removed, changes[i].Ranges = added, removed, ranges
		}
	}

	if len(touched) == 0 {
		return ""
	}

	out := "gofmt and goimports also rewrote " + strings.Join(touched, ", ") +
		", so the file no longer holds the text you sent."

	if len(touched) == 1 && len(changes) == 1 && len(changes[0].Ranges) > 0 {
		if body := regionAfter(edits[0].Path, changes[0].Ranges[0]); body != "" {
			out += " It now reads:\n" + body
		}
	}

	return out
}

// regionAfter is the changed span as it stands on disk, numbered the way
// read numbers a file so the same anchor rules apply to it. Showing it
// costs a few lines once; telling the caller to read the file again costs a
// whole turn, which is what 6 of 18 recorded anchor misses went on.
func regionAfter(path string, r tool.LineRange) string {
	//nolint:gosec // the path is one this call just edited through Scope
	src, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(src), "\n")

	start := max(r.Start, 1)
	end := min(r.End, len(lines))

	if start > end || end-start >= maxEchoedLines {
		return ""
	}

	return numbered(lines[start-1:end], start)
}

// staleAnchor answers an anchor drawn from a read this run has since
// written over, with the text where the harness already has it.
func staleAnchor(edits []edit.FileEdit, anchor string, err error, stale string) tool.Result {
	if body := currentRegion(edits, anchor, err); body != "" {
		return tool.Fail(tool.CauseNoMatch,
			"%v\n\nYou have edited %s since you last read it, so an anchor taken from that "+
				"read no longer matches. That part of the file now reads:\n%s", err, stale, body)
	}

	return tool.Fail(tool.CauseNoMatch,
		"%v\n\nYou have edited %s since you last read it, so an anchor taken from that "+
			"read no longer matches. Read it again before anchoring, or use declare to "+
			"write the whole declaration by name.", err, stale)
}

// currentRegion is what the file now holds where the anchor came closest,
// numbered the way read numbers a file. It answers a stale anchor with the
// text rather than with an instruction to go and read it: the harness
// already has the bytes, and the read it would ask for costs a whole turn.
// Six of eighteen recorded anchor misses spent one, and the twelve that did
// not guessed again instead.
func currentRegion(edits []edit.FileEdit, anchor string, err error) string {
	var notFound *edit.NotFoundError
	if !errors.As(err, &notFound) || notFound.CandidateLine == 0 || len(edits) != 1 {
		return ""
	}

	src, readErr := os.ReadFile(edits[0].Path)
	if readErr != nil {
		return ""
	}

	lines := strings.Split(string(src), "\n")

	start := notFound.CandidateLine
	if start < 1 || start > len(lines) {
		return ""
	}

	end := min(start+len(strings.Split(anchor, "\n"))-1, len(lines))
	if end-start >= maxEchoedLines {
		return ""
	}

	return numbered(lines[start-1:end], start)
}
