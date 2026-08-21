package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

const maxListedFiles = 200

// maxListedDirs caps the rollup a listing falls back to when it has more
// files than it may print.
const maxListedDirs = 60

// skipListDirs are never descended into. They hold no source a model asks
// for and one of them, .git, is large enough to swamp a listing on its own.
var skipListDirs = map[string]bool{".git": true, ".jj": true, "node_modules": true}

var listSchema = buildSchema(map[string]schemaProperty{
	"dir": {
		Type: schemaTypeString,
		Description: "Directory to list, relative to the project root, or several separated " +
			"by commas. Omit it to list from the root. A path outside the root is refused.",
	},
	"pattern": {
		Type: schemaTypeString,
		Description: "Glob the file must match, such as `*.go`. A pattern holding a slash " +
			"matches the whole path below dir (`internal/*/*.go`), one without it matches the " +
			"file name alone. Omit it to list every file.",
	},
}, "dir")

// List reports the files under a directory, recursively, so a model can see
// what a project holds without spending a shell call on `find` or `ls`.
type List struct {
	root string
}

// NewList builds a List tool over root.
func NewList(root string) *List { return &List{root: root} }

// Name implements tool.Tool.
func (*List) Name() string { return "list" }

// Description implements tool.Tool.
func (*List) Description() string {
	return "List the files under a project directory, recursively, optionally filtered by a " +
		"glob. Use it to find out what exists; use search to find code by name or text. " +
		"Refuses paths outside the project root."
}

// Schema implements tool.Tool.
func (*List) Schema() json.RawMessage { return listSchema }

type listInput struct {
	Dir     string `json:"dir"`
	Pattern string `json:"pattern"`
}

// Run implements tool.Tool.
func (l *List) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("list: %w", err)
	}

	var in listInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	if in.Pattern != "" {
		if _, err := path.Match(in.Pattern, "probe"); err != nil {
			return tool.Errorf("pattern %q is not a valid glob: %v", in.Pattern, err), nil
		}
	}

	dirs := readPaths(input, "dir", in.Dir)
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	blocks := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		abs, err := resolvePath(l.root, dir)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}

		found, err := walkMatching(abs, in.Pattern)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}

		blocks = append(blocks, formatListing(dir, in.Pattern, found))
	}

	return tool.Result{Content: strings.Join(blocks, "\n\n")}, nil
}

func walkMatching(root, pattern string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("listing %s: %w", p, err)
		}
		if d.IsDir() {
			if p != root && skipListDirs[d.Name()] {
				return filepath.SkipDir
			}

			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", p, err)
		}
		rel = filepath.ToSlash(rel)
		if matchesGlob(pattern, rel) {
			found = append(found, rel)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}

	return found, nil
}

// matchesGlob matches a slash-holding pattern against the whole relative
// path and any other pattern against the file name, because `*.go` from a
// model means "a Go file anywhere below here" and path.Match would read the
// slashes as a depth constraint it did not intend.
func matchesGlob(pattern, rel string) bool {
	if pattern == "" {
		return true
	}

	subject := rel
	if !strings.Contains(pattern, "/") {
		subject = path.Base(rel)
	}
	ok, err := path.Match(pattern, subject)

	return err == nil && ok
}

func formatListing(dir, pattern string, found []string) string {
	what := "files"
	if pattern != "" {
		what = fmt.Sprintf("files matching %q", pattern)
	}

	if len(found) == 0 {
		return fmt.Sprintf("no %s under %s", what, dir)
	}

	var b strings.Builder

	if len(found) > maxListedFiles {
		fmt.Fprintf(&b, "%d %s under %s, too many to name. Files per directory:\n", len(found), what, dir)
		b.WriteString(rollup(found))
		b.WriteString("\nNarrow with dir or pattern to see file names.")

		return b.String()
	}

	fmt.Fprintf(&b, "%d %s under %s:\n", len(found), what, dir)
	b.WriteString(strings.Join(found, "\n"))

	return b.String()
}

// rollup summarizes an oversized listing as a count per directory. Printing
// the first 200 paths instead answers a question about the layout with
// whichever directory sorts first, which on this repo is a run of dot
// directories the model was not asking about.
func rollup(found []string) string {
	counts := make(map[string]int, len(found))
	for _, f := range found {
		counts[path.Dir(f)]++
	}

	dirs := make([]string, 0, len(counts))
	for d := range counts {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var b strings.Builder

	shown := dirs
	if len(shown) > maxListedDirs {
		shown = shown[:maxListedDirs]
	}
	for _, d := range shown {
		fmt.Fprintf(&b, "  %s/  %d\n", d, counts[d])
	}

	if len(shown) < len(dirs) {
		fmt.Fprintf(&b, "  ... [%d more directories] ...\n", len(dirs)-len(shown))
	}

	return b.String()
}
