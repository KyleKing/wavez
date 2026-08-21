package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/kyleking/wavez/internal/codeintel"
)

// declaration is where a symbol is declared: the absolute path, the
// zero-based line and UTF-16 column a language server addresses, and the
// indexed line range of the declaration itself.
type declaration struct {
	path   string
	line   int
	column int
	start  int
	end    int
}

// locate finds the one declaration of name, optionally narrowed to a file or
// directory. A name declared in several places is an error listing them
// rather than a guess: acting on the wrong one is a change the caller then
// has to find and undo.
func locate(ctx context.Context, index Index, root, name, path string) (declaration, error) {
	results, _, err := index.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: name})
	if err != nil {
		return declaration{}, fmt.Errorf("searching for %s: %w", name, err)
	}

	var all, found []codeintel.Symbol

	for i := range results {
		sym := results[i].Symbol
		if sym == nil || sym.Name != name {
			continue
		}

		all = append(all, *sym)

		if under(sym.FilePath, path) {
			found = append(found, *sym)
		}
	}

	switch len(found) {
	case 0:
		return declaration{}, fmt.Errorf("%w: %s%s%s",
			ErrSymbolNotIndexed, name, inFile(path), elsewhere(all))
	case 1:
		return position(root, found[0], name)
	default:
		return declaration{}, fmt.Errorf("%w: %s declares %s; name one with path",
			ErrAmbiguousSymbol, strings.Join(declaringFiles(found), ", "), name)
	}
}

// under reports whether the symbol's file is at or below the caller's path,
// which may name a file or a directory. A model narrowing by package writes
// the directory, and refusing that would be refusing the obvious reading.
func under(file, path string) bool {
	if path == "" {
		return true
	}

	clean := strings.TrimSuffix(filepath.Clean(path), string(filepath.Separator))

	return file == clean || strings.HasPrefix(file, clean+string(filepath.Separator))
}

func inFile(path string) string {
	if path == "" {
		return ""
	}

	return " under " + path
}

// elsewhere names where the symbol actually is, so a caller that narrowed to
// the wrong place can correct in one turn rather than searching again.
func elsewhere(all []codeintel.Symbol) string {
	if len(all) == 0 {
		return ""
	}

	return "; it is declared in " + strings.Join(declaringFiles(all), ", ")
}

func declaringFiles(syms []codeintel.Symbol) []string {
	seen := make(map[string]bool, len(syms))
	out := make([]string, 0, len(syms))

	for i := range syms {
		if seen[syms[i].FilePath] {
			continue
		}

		seen[syms[i].FilePath] = true

		out = append(out, syms[i].FilePath)
	}

	sort.Strings(out)

	return out
}

// position turns an indexed symbol into the exact spot the name starts. The
// index records the line a declaration begins on, and a server needs the
// identifier itself, so the name is found within that line.
func position(root string, sym codeintel.Symbol, name string) (declaration, error) {
	abs := filepath.Join(root, sym.FilePath)

	body, err := os.ReadFile(abs) //nolint:gosec // the path comes from this project's own index
	if err != nil {
		return declaration{}, fmt.Errorf("reading %s: %w", sym.FilePath, err)
	}

	lines := strings.Split(string(body), "\n")
	for i := sym.StartLine - 1; i < len(lines) && i < sym.EndLine; i++ {
		if i < 0 {
			continue
		}

		if col, ok := identifierColumn(lines[i], name); ok {
			return declaration{path: abs, line: i, column: col, start: sym.StartLine, end: sym.EndLine}, nil
		}
	}

	return declaration{}, fmt.Errorf("%w: %s at %s:%d", ErrDeclarationMoved, name, sym.FilePath, sym.StartLine)
}

// identifierColumn finds name in line as a whole word and reports its UTF-16
// column, which is what the protocol counts in.
func identifierColumn(line, name string) (int, bool) {
	for at := 0; ; {
		i := strings.Index(line[at:], name)
		if i < 0 {
			return 0, false
		}

		i += at
		if wholeWord(line, i, len(name)) {
			return len(utf16.Encode([]rune(line[:i]))), true
		}

		at = i + len(name)
	}
}

func wholeWord(line string, start, width int) bool {
	if start > 0 && isNameByte(line[start-1]) {
		return false
	}

	end := start + width

	return end >= len(line) || !isNameByte(line[end])
}

func isNameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// relativeTo names a path the way the project talks about it, falling back
// to the absolute path when it lies outside root.
func relativeTo(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}

	return rel
}
