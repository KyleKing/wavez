package hook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/session"
)

// renameBudget bounds the search for the declaration a text rename names.
// Every shell command waits on this hook.
const renameBudget = 2 * time.Second

// maxRenames is how many renames of one command are looked up, and
// maxGlobbed how many files one of its globs may name.
const (
	maxRenames = 3
	maxGlobbed = 200
)

// deniedSuffix names the file beside a session's state that lists the
// renames already denied in it, one "old new" pair a line.
const deniedSuffix = ".renames"

const (
	deniedFileMode = 0o600
	renameProgram  = "gopls"
	moduleFile     = "go.mod"
)

// reservedNames are the declarations `wavez rename` will not rename, and
// the names it will not rename anything to.
var reservedNames = []string{"main", "init", "_"}

// running answers a PreToolUse event for the shell tool. A command that
// renames a Go identifier by rewriting text is denied once per session and
// pair, with the `wavez rename` line that does the same through gopls. The
// same command sent again runs: the agent may have a reason, and a rule
// that is wrong must cost one round and no more.
//
// It speaks up only when the suggestion can work: gopls is installed, the
// files the command rewrites are Go files of this project, and their own
// packages declare the old name and not yet the new one. It has no way to
// fail, because anything it cannot establish lets the command through in
// silence.
func (h *Handler) running(ctx context.Context, ev Event, rec *record) output {
	rec.Tool = ev.ToolName
	if ev.ToolName != ShellTool {
		return output{}
	}

	if res, held := h.committing(ctx, ev, rec); held {
		return res
	}

	renames := textRenames(ev.ToolInput.Command)
	if len(renames) == 0 {
		return output{}
	}

	if _, err := h.lookPath(renameProgram); err != nil {
		rec.Note = "text rename let through: no " + renameProgram

		return output{}
	}

	// A pair already denied in this session is let through before the
	// module is searched, so a repeat costs one small read.
	list := deniedList{store: session.NewStore(rec.Root), path: deniedPath(rec.Root, ev.SessionID)}
	renames = list.unseen(renames)

	if len(renames) == 0 {
		rec.Note = "text rename let through: already denied once"

		return output{}
	}

	ctx, cancel := context.WithTimeout(ctx, renameBudget)
	defer cancel()

	var (
		found []renamePair
		lines []string
	)

	for _, rename := range renames[:min(len(renames), maxRenames)] {
		sites := renameSites(ctx, rec.Root, ev.Cwd, rename)
		if len(sites) == 0 {
			continue
		}

		found = append(found, rename.pair)
		lines = append(lines, suggestion(rename.pair, sites)...)
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

// renameSites returns the declarations a text rename is really renaming, or
// none when it is not surely a rename `wavez rename` could make instead.
//
// With files named, the declaration has to be in the packages those files
// are in, and one of the files has to declare it or use it. With files fed
// from a pipeline that filtered on Go source, the whole module is in scope.
// Either way a new name the package already declares means the command is
// switching to something that exists, not renaming.
func renameSites(ctx context.Context, root, cwd string, rename textRename) []site {
	if slices.Contains(reservedNames, rename.pair.old) || slices.Contains(reservedNames, rename.pair.new) {
		return nil
	}

	base := filepath.Join(cwd, filepath.FromSlash(rename.dir))
	if path.IsAbs(rename.dir) {
		base = filepath.FromSlash(rename.dir)
	}

	files := goFilesOf(root, base, rename.files)
	if len(files) == 0 && !rename.module {
		return nil
	}

	start := base
	if len(files) > 0 {
		start = filepath.Dir(files[0])
	}

	module, ok := nearest(start, moduleFile)
	if !ok {
		return nil
	}

	var named []string

	for _, file := range files {
		if rel, ok := under(module, file); ok {
			named = append(named, rel)
		}
	}

	if len(named) == 0 && !rename.module {
		return nil
	}

	sites := candidateSites(ctx, module, named, rename)
	if introduced(ctx, module, sites, rename.pair.new) {
		return nil
	}

	return sites
}

// candidateSites finds the declarations of the old name that are in scope
// and that the command's files reach.
func candidateSites(ctx context.Context, module string, named []string, rename textRename) []site {
	var sites []site

	switch {
	case rename.qualifier != "":
		// `bench.Read` is declared in a package called bench, wherever the
		// file that says so is.
		for _, s := range declarations(ctx, module, rename.pair.old) {
			if path.Base(s.dir()) == rename.qualifier && !s.member() {
				sites = append(sites, s)
			}
		}
	case len(named) == 0:
		return declarations(ctx, module, rename.pair.old)
	default:
		for _, dir := range dirsOf(named) {
			sites = append(sites, declaredInDir(ctx, module, dir, rename.pair.old)...)
		}
	}

	if len(named) == 0 {
		return sites
	}

	tree := os.DirFS(module)

	return slices.DeleteFunc(sites, func(s site) bool {
		return !slices.Contains(named, s.file) && !slices.ContainsFunc(named, func(file string) bool {
			return mentions(tree, file, rename.pair.old, s.member() || rename.qualifier != "")
		})
	})
}

// introduced reports whether a package that declares the old name already
// declares the new one.
func introduced(ctx context.Context, module string, sites []site, name string) bool {
	return slices.ContainsFunc(sites, func(s site) bool {
		return len(declaredInDir(ctx, module, s.dir(), name)) > 0
	})
}

// goFilesOf resolves the file arguments of a command run in base to the Go
// files they name that exist inside the project at root. A glob is expanded
// the way the shell would.
func goFilesOf(root, base string, args []string) []string {
	var files []string

	for _, arg := range args {
		full := filepath.FromSlash(arg)
		if !filepath.IsAbs(full) {
			full = filepath.Join(base, full)
		}

		matches := []string{full}

		if strings.ContainsAny(arg, globChars) {
			globbed, err := filepath.Glob(full)
			if err != nil {
				continue
			}

			matches = globbed[:min(len(globbed), maxGlobbed)]
		}

		for _, match := range matches {
			if !strings.HasSuffix(match, ".go") || !isRegular(match) {
				continue
			}

			if _, inside := relative(root, root, match); inside && !slices.Contains(files, match) {
				files = append(files, match)
			}
		}
	}

	return files
}

func isRegular(name string) bool {
	info, err := os.Stat(name)

	return err == nil && info.Mode().IsRegular()
}

// dirsOf returns the distinct directories of slash-separated files.
func dirsOf(files []string) []string {
	var dirs []string

	for _, file := range files {
		if dir := path.Dir(file); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// suggestion is the command lines that rename one pair, one per place the
// old name is declared.
func suggestion(pair renamePair, sites []site) []string {
	var lines []string

	if len(sites) > 1 {
		lines = append(lines, "("+pair.old+" is declared more than once; use the line for the one you mean)")
	}

	for _, s := range sites[:min(len(sites), maxTargets)] {
		lines = append(lines, "  wavez rename "+quote(s.target())+" "+pair.new)
	}

	return lines
}

// deniedList is the renames already denied in one session: a file of its
// own beside the session's state, appended to and never rewritten.
type deniedList struct {
	store *session.Store
	path  string
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

// unseen returns the renames the list does not hold. A list that cannot be
// read holds everything, which lets every command through.
func (l deniedList) unseen(renames []textRename) []textRename {
	if l.path == "" {
		return nil
	}

	known, err := os.ReadFile(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}

	seen := strings.Split(string(known), "\n")

	var out []textRename

	for _, rename := range renames {
		if !slices.Contains(seen, rename.pair.line()) {
			out = append(out, rename)
		}
	}

	return out
}

// add appends pairs to the list.
func (l deniedList) add(pairs []renamePair) error {
	if err := l.store.Prepare(); err != nil {
		return fmt.Errorf("recording denied renames: %w", err)
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
