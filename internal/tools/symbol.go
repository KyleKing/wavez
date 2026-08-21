package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
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
	results, err := searchWidening(ctx, index, name)
	if err != nil {
		return declaration{}, err
	}

	var all, found, near []codeintel.Symbol

	for i := range results {
		sym := results[i].Symbol
		if sym == nil {
			continue
		}

		if sym.Name != name {
			if similar(sym.Name, name) {
				near = append(near, *sym)
			}

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
			ErrSymbolNotIndexed, name, inFile(path), orNearby(all, near))
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

// orNearby says where the symbol actually is, or failing that what the index
// holds that is close to the name asked for. Measured: a run that deleted a
// function correctly then guessed `ApplyToFileTest` for its test, was told
// only that nothing by that name is indexed, and spent the rest of itself
// searching for a name that never existed. The candidates were already in
// the search result the refusal was built from.
func orNearby(all, near []codeintel.Symbol) string {
	if len(all) > 0 {
		return "; it is declared in " + strings.Join(declaringFiles(all), ", ")
	}

	if len(near) == 0 {
		return ""
	}

	return "; the index holds " + strings.Join(names(near), ", ")
}

// searchWidening looks the name up, and when the index holds nothing at all
// for it, drops one trailing CamelCase word at a time and looks again. A
// guessed name is usually a real one with something appended: measured, a run
// that had just deleted `ApplyToFile` guessed `ApplyToFileTest` for its test,
// found nothing, and spent the rest of itself searching for a name that never
// existed. Trimming to `ApplyToFile` finds what is really there, and the
// caller's own exact-match filter still decides what counts.
func searchWidening(ctx context.Context, index Index, name string) ([]codeintel.SearchResult, error) {
	for query := name; query != ""; query = dropLastWord(query) {
		results, _, err := index.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: query})
		if err != nil {
			return nil, fmt.Errorf("searching for %s: %w", query, err)
		}

		if len(results) > 0 {
			return results, nil
		}
	}

	return nil, nil
}

// dropLastWord removes the final CamelCase word, returning "" when one word
// is all that is left, since a single word widened further is a different
// question than the one asked.
func dropLastWord(s string) string {
	for i := len(s) - 1; i > 0; i-- {
		if unicode.IsUpper(rune(s[i])) {
			return s[:i]
		}
	}

	return ""
}

// similar reports whether one name contains the other, which is what a
// near miss looks like: TestApplyToFile against ApplyToFile, or a guessed
// suffix against the real name.
func similar(indexed, asked string) bool {
	a, b := strings.ToLower(indexed), strings.ToLower(asked)

	return strings.Contains(a, b) || strings.Contains(b, a)
}

// names lists a few candidates with where they live, newest match first and
// capped so a refusal stays a sentence.
func names(syms []codeintel.Symbol) []string {
	const most = 3

	out := make([]string, 0, most)
	for i := range syms {
		if len(out) == most {
			break
		}

		out = append(out, fmt.Sprintf("%s (%s)", syms[i].Name, syms[i].FilePath))
	}

	return out
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
