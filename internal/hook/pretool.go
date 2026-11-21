package hook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/session"
)

// renameBudget bounds the search for the declaration a text rename names.
// Every shell command waits on this hook.
const renameBudget = 2 * time.Second

// maxRenames is how many renames of one command are looked up.
const maxRenames = 3

// deniedSuffix names the file beside a session's state that lists the
// renames already denied in it, one "old new" pair a line.
const deniedSuffix = ".renames"

const (
	deniedFileMode = 0o600
	deniedDirMode  = 0o750
	renameProgram  = "gopls"
	moduleFile     = "go.mod"
)

// running answers a PreToolUse event for the shell tool. A command that
// renames a Go identifier by rewriting text is denied once per session and
// pair, with the `wavez rename` line that does the same through gopls. The
// same command sent again runs: the agent may have a reason, and a rule
// that is wrong must cost one round and no more.
//
// It speaks up only when the suggestion can work: the directory is inside
// a Go module, gopls is installed, and the module declares the old name.
// It has no way to fail, because anything it cannot establish lets the
// command through in silence.
func (h *Handler) running(ctx context.Context, ev event, rec *record) output {
	rec.Tool = ev.ToolName
	if ev.ToolName != ShellTool {
		return output{}
	}

	pairs := textRenames(ev.ToolInput.Command)
	if len(pairs) == 0 {
		return output{}
	}

	module, ok := nearest(filepath.Clean(ev.Cwd), moduleFile)
	if !ok {
		return output{}
	}

	if _, err := h.lookPath(renameProgram); err != nil {
		rec.Note = "text rename let through: no " + renameProgram

		return output{}
	}

	// A pair already denied in this session is let through before the
	// module is searched, so a repeat costs one small read.
	list := deniedList{path: deniedPath(rec.Root, ev.SessionID)}
	pairs = list.unseen(pairs)

	if len(pairs) == 0 {
		rec.Note = "text rename let through: already denied once"

		return output{}
	}

	ctx, cancel := context.WithTimeout(ctx, renameBudget)
	defer cancel()

	var (
		found []renamePair
		lines []string
	)

	for _, pair := range pairs[:min(len(pairs), maxRenames)] {
		targets := declarations(ctx, module, pair.old)
		if len(targets) == 0 {
			continue
		}

		found = append(found, pair)
		lines = append(lines, suggestion(pair, targets)...)
	}

	if len(found) == 0 {
		return output{}
	}

	// A denial that could not be recorded would repeat forever, so a
	// failed write denies nothing.
	if err := list.add(found); err != nil {
		rec.Note = "text rename let through: " + err.Error()

		return output{}
	}

	rec.Note = "text rename denied: " + found[0].old + " to " + found[0].new

	return deny("wavez: this renames a Go symbol by rewriting text. Run this instead. It renames the " +
		"declaration and every reference through gopls, compiles the result, and lists the comments " +
		"and strings that still say the old name:\n" + strings.Join(lines, "\n") +
		"\nThe command you sent will run if you send it again.")
}

// suggestion is the command lines that rename one pair, one per place the
// old name is declared.
func suggestion(pair renamePair, targets []string) []string {
	var lines []string

	if len(targets) > 1 {
		lines = append(lines, "("+pair.old+" is declared more than once; use the line for the one you mean)")
	}

	for _, target := range targets {
		lines = append(lines, "  wavez rename "+quote(target)+" "+pair.new)
	}

	return lines
}

// deniedList is the renames already denied in one session: a file of its
// own beside the session's state, appended to and never rewritten.
type deniedList struct {
	path string
}

// deniedPath is where a session's list is kept, and "" for a session id
// that cannot name a file.
func deniedPath(root, id string) string {
	if id == "" || !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) {
		return ""
	}

	return strings.TrimSuffix(session.NewStore(root).Path(id), ".json") + deniedSuffix
}

func (pair renamePair) line() string { return pair.old + " " + pair.new }

// unseen returns the pairs the list does not hold. A list that cannot be
// read holds everything, which lets every command through.
func (l deniedList) unseen(pairs []renamePair) []renamePair {
	if l.path == "" {
		return nil
	}

	known, err := os.ReadFile(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}

	seen := strings.Split(string(known), "\n")

	var out []renamePair

	for _, pair := range pairs {
		if !slices.Contains(seen, pair.line()) {
			out = append(out, pair)
		}
	}

	return out
}

// add appends pairs to the list.
func (l deniedList) add(pairs []renamePair) error {
	if err := os.MkdirAll(filepath.Dir(l.path), deniedDirMode); err != nil {
		return fmt.Errorf("creating session directory: %w", err)
	}

	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, deniedFileMode)
	if err != nil {
		return fmt.Errorf("opening denied renames: %w", err)
	}

	for _, pair := range pairs {
		if _, writeErr := file.WriteString(pair.line() + "\n"); writeErr != nil {
			err = writeErr

			break
		}
	}

	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("recording denied renames: %w", err)
	}

	return nil
}
