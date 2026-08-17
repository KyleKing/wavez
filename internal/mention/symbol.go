package mention

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel"
)

const (
	symbolSearchLimit = 50
	maxSymbolMatches  = 5
	maxNearMisses     = 3
	maxSignatureLen   = 100
	maxDocLen         = 120
)

// resolveSymbol expands ref as a symbol. It emits each match's kind,
// location, signature, and first doc line rather than its body: on an 8k
// window a body the model did not ask for crowds out the thread, while a
// location and a signature are enough to decide whether reading it is worth
// a turn.
//
// Several matches never collapse to one. Picking the first would be a guess
// the model cannot see, so an ambiguous reference lists its candidates and
// says none was chosen.
func (e *Expander) resolveSymbol(ctx context.Context, ref string) expansion {
	if e.index == nil {
		return note(ref, fmt.Sprintf(
			"no file at %s and no code index is attached, so a symbol cannot resolve; left as literal text", ref))
	}

	name := ref
	if i := strings.LastIndex(ref, "."); i >= 0 {
		name = ref[i+1:]
	}

	results, stats, err := e.index.Search(ctx, codeintel.SearchQuery{
		Mode:  codeintel.SearchFuzzy,
		Text:  name,
		Limit: symbolSearchLimit,
	})
	if err != nil {
		return note(ref, fmt.Sprintf(
			"the code index could not be queried (%v); left as literal text", err))
	}

	found := partitionMatches(results, ref)
	if len(found.exact) == 0 {
		return note(ref, missReason(ref, stats, found.near))
	}

	return symbolExpansion(ref, found.exact)
}

func symbolExpansion(ref string, matches []codeintel.Symbol) expansion {
	shown := matches
	if len(shown) > maxSymbolMatches {
		shown = shown[:maxSymbolMatches]
	}

	var b strings.Builder
	if len(matches) == 1 {
		fmt.Fprintf(&b, "@%s (symbol):\n", ref)
	} else {
		fmt.Fprintf(&b, "@%s (symbol, %d matches, none chosen; name one as package.Name or read one):\n",
			ref, len(matches))
	}

	for i := range shown {
		writeSymbolLines(&b, &shown[i])
	}
	if len(matches) > len(shown) {
		fmt.Fprintf(&b, "  [%d more matches not shown]\n", len(matches)-len(shown))
	}

	detail := fmt.Sprintf("%s %s:%d-%d", matches[0].Kind, matches[0].FilePath, matches[0].StartLine, matches[0].EndLine)
	if len(matches) > 1 {
		detail = fmt.Sprintf("%d matches, none chosen", len(matches))
	}

	return expansion{
		mentions: []Mention{{Ref: ref, Kind: KindSymbol, Detail: detail}},
		section:  strings.TrimSuffix(b.String(), "\n"),
		handled:  true,
	}
}

func writeSymbolLines(b *strings.Builder, sym *codeintel.Symbol) {
	fmt.Fprintf(b, "  %s %s %s:%d-%d", sym.Kind, sym.Name, sym.FilePath, sym.StartLine, sym.EndLine)
	if sig := flatten(sym.Signature, maxSignatureLen); sig != "" {
		fmt.Fprintf(b, " %s", sig)
	}
	b.WriteString("\n")
	if doc := flatten(firstLine(sym.Doc), maxDocLen); doc != "" {
		fmt.Fprintf(b, "    doc: %s\n", doc)
	}
}

// missReason names which kind of empty this is. Told that an unindexed tree
// and a query that matched nothing are both "not found", a model narrows a
// reference that could not have matched anything.
func missReason(ref string, stats codeintel.IndexStats, near []string) string {
	if stats.FilesScanned == 0 {
		return fmt.Sprintf("no file at %s and the code index covers no files in this project, "+
			"so a symbol cannot resolve here; left as literal text, use shell with rg", ref)
	}

	reason := fmt.Sprintf("no file at %s and no indexed symbol named %s across %d indexed files; "+
		"left as literal text", ref, ref, stats.FilesScanned)
	if len(near) > 0 {
		reason += fmt.Sprintf(" (closest indexed names: %s)", strings.Join(near, ", "))
	}

	return reason
}

// symbolMatches holds a fuzzy result set split into exact matches for a
// reference and the near misses worth naming when there are none.
type symbolMatches struct {
	exact []codeintel.Symbol
	near  []string
}

// partitionMatches preserves fuzzy order, so one prompt expands the same way
// twice.
func partitionMatches(results []codeintel.SearchResult, ref string) symbolMatches {
	var found symbolMatches

	seenNear := map[string]bool{}
	for _, r := range results {
		if r.Symbol == nil {
			continue
		}
		if matchesRef(r.Symbol, ref) {
			found.exact = append(found.exact, *r.Symbol)

			continue
		}
		if len(found.near) < maxNearMisses && !seenNear[r.Symbol.Name] {
			seenNear[r.Symbol.Name] = true
			found.near = append(found.near, r.Symbol.Name)
		}
	}

	return found
}

// matchesRef accepts a bare name only on an exact match, and a qualified
// `pkg.Name` when the qualifier names the symbol's directory or its file.
// A reference holding a path separator is never a symbol.
func matchesRef(sym *codeintel.Symbol, ref string) bool {
	if strings.Contains(ref, "/") {
		return false
	}
	if sym.Name == ref {
		return true
	}

	i := strings.LastIndex(ref, ".")
	if i < 0 {
		return false
	}

	qualifier, name := ref[:i], ref[i+1:]
	if sym.Name != name {
		return false
	}

	file := path.Base(sym.FilePath)

	return qualifier == path.Base(path.Dir(sym.FilePath)) ||
		qualifier == strings.TrimSuffix(file, path.Ext(file))
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}

	return ""
}

// flatten keeps one symbol to one line: a tree-sitter signature carries the
// whole declaration, so a struct's fields arrive with it.
func flatten(text string, limit int) string {
	flat := []rune(strings.Join(strings.Fields(text), " "))
	if len(flat) > limit {
		return string(flat[:limit]) + "…"
	}

	return string(flat)
}
